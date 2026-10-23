package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

type savedFile struct {
	Target string `json:"target"`
	Saved  string `json:"saved,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	SHA256 string `json:"sha256,omitempty"`
}
type installation struct {
	TaskXML string `json:"task_xml,omitempty"`

	Manifest   Manifest    `json:"manifest"`
	Files      []savedFile `json:"files"`
	WasActive  bool        `json:"was_active"`
	WasEnabled bool        `json:"was_enabled"`
	Signature  string      `json:"signature,omitempty"`
}

// verified is what a bundle held when it was checked: each payload file's
// bytes, by name, and the service definition. Installing writes these
// bytes and never reads the bundle again, so whoever can write to the
// bundle directory cannot change a file between its check and its copy.
type verified struct {
	files   map[string][]byte
	service []byte
}

func loadManifest(dir string) (Manifest, error) {
	m, _, e := loadBundle(dir)
	return m, e
}

func loadBundle(dir string) (Manifest, verified, error) {
	v := verified{files: map[string][]byte{}}
	m, e := checkBundle(dir, &v)
	return m, v, e
}

// Review returns a receipt to keep outside the bundle until installation. The
// canonical manifest covers every payload hash and installation option; the
// generated service bytes are bound too. Colocated checksums alone are mutable.
func Review(dir string) (Manifest, string, error) {
	m, bundle, err := loadBundle(dir)
	if err != nil {
		return m, "", err
	}
	return m, bundleDigest(m, bundle), nil
}

func bundleDigest(m Manifest, bundle verified) string {
	m.Files = slices.Clone(m.Files)
	slices.SortFunc(m.Files, func(a, b File) int { return strings.Compare(a.Name, b.Name) })
	raw, _ := json.Marshal(struct {
		Manifest      Manifest `json:"manifest"`
		ServiceSHA256 string   `json:"service_sha256"`
	}{m, secure.Hash(string(bundle.service))})
	return secure.Hash(string(raw))
}

func checkBundle(dir string, v *verified) (Manifest, error) {
	var m Manifest
	b, e := secure.ReadBounded(filepath.Join(dir, "manifest.json"), 1<<20)
	if e != nil {
		return m, e
	}
	if e = secure.Decode(bytes.NewReader(b), &m, 1<<20); e != nil {
		return m, e
	}
	if m.Version == "" {
		return m, errors.New("missing bundle version")
	}
	if m.Schema != 1 || m.Platform != runtime.GOOS || m.Role != "server" && m.Role != "worker" && m.Role != "client" || !config.ID(m.Project) || !cleanTarget(m.InstallDir, m.Platform) {
		return m, errors.New("invalid or non-native installation manifest")
	}
	if m.Role != "client" && (!cleanTarget(m.DataDir, m.Platform) || m.Platform != "windows" && (!config.ID(m.User) || m.User == "root")) {
		return m, errors.New("invalid data directory or service user")
	}
	if len(m.Files) < 3 || len(m.Files) > 8 {
		return m, errors.New("invalid installation file count")
	}
	allowed := map[string]bool{"bin/topo": runtime.GOOS != "windows", "bin/topo.exe": runtime.GOOS == "windows", "ca.pem": true, "credentials.json": true, "worker-credentials.json": m.Role == "worker", "auth.json": m.Role == "server", "server.pem": m.Role == "server", "server-key.pem": m.Role == "server", "topology.json": m.Role != "client"}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !allowed[f.Name] || seen[f.Name] || f.Source != "payload/"+f.Name || f.Mode != 0600 && f.Mode != 0644 && f.Mode != 0755 {
			return m, errors.New("unexpected manifest payload")
		}
		private := f.Name == "credentials.json" || f.Name == "worker-credentials.json" || f.Name == "auth.json" || f.Name == "server-key.pem" || f.Name == "topology.json"
		if f.Private != private || private && f.Mode != 0600 {
			return m, errors.New("invalid private payload permissions")
		}
		seen[f.Name] = true
		b, e := secure.ReadBounded(filepath.Join(dir, filepath.FromSlash(f.Source)), 64<<20)
		if e != nil || secure.Hash(string(b)) != f.SHA256 {
			return m, errors.New("installation payload checksum mismatch")
		}
		v.files[f.Name] = b
	}
	required := []string{"ca.pem"}
	binaryName := "bin/topo"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	required = append(required, binaryName)
	if m.Role == "worker" {
		required = append(required, "worker-credentials.json", "topology.json")
	} else {
		required = append(required, "credentials.json")
	}
	if m.Role == "server" {
		required = append(required, "auth.json", "server.pem", "server-key.pem", "topology.json")
	}
	for _, name := range required {
		if !seen[name] {
			return m, errors.New("missing required installation payload")
		}
	}
	if m.Role == "client" && (m.Service != "" || m.ServiceFile != "") {
		return m, errors.New("client cannot install a service")
	}
	if m.Role != "client" {
		// The topology checked is the one whose checksum matched, not a
		// second read of the file.
		c, e := config.Decode(bytes.NewReader(v.files["topology.json"]))
		if e != nil {
			return m, e
		}
		if c.Project != m.Project {
			return m, errors.New("topology project differs from manifest")
		}
		if err := validateObserver(m.Role, m.Observer, c); err != nil {
			return m, err
		}
		name, file, expected, e := service(Options{Server: m.Server, Role: m.Role, Platform: m.Platform, User: m.User, InstallDir: m.InstallDir, DataDir: m.DataDir, Observer: m.Observer, Config: c})
		if e != nil || file != m.ServiceFile || name != m.Service {
			return m, errors.New("unexpected service destination")
		}
		raw, e := secure.ReadBounded(filepath.Join(dir, "service"), 1<<20)
		if e != nil || !bytes.Equal(raw, expected) {
			return m, errors.New("service differs from reviewed manifest")
		}
		v.service = raw
	}
	return m, nil
}

// Install verifies an immutable bundle, backs up replaced files and restores
// them if installation/startup fails. Existing collector/outbox data is untouched.
// checked, when set, is told of a bundle once it has been checked and
// before anything is installed from it; tests change the bundle there.
var checked func(dir string)

func Install(ctx context.Context, dir string, dry bool, expected string) (Manifest, string, error) {
	m, bundle, e := loadBundle(dir)
	if e != nil {
		return m, "", e
	}
	if dry {
		return m, "", nil
	}
	if expected == "" || expected != bundleDigest(m, bundle) {
		return m, "", errors.New("bundle review receipt missing or changed; review with --dry-run and supply --bundle-sha256 from that review")
	}
	if e = trustedBundleSource(ctx, dir, m); e != nil {
		return m, "", e
	}
	if checked != nil {
		checked(dir)
	}
	if m.Role != "client" && runtime.GOOS != "windows" && os.Geteuid() != 0 {
		return m, "", errors.New("service installation requires an administrator; service identity remains unprivileged")
	}
	if m.Role != "client" {
		if old, e := config.Load(filepath.Join(m.InstallDir, "topology.json")); e == nil && old.Project != m.Project {
			return m, "", errors.New("existing installation belongs to a different project")
		}
	}
	if m.Role == "worker" {
		pending := filepath.Join(m.DataDir, "pending")
		if err := checkPendingOutbox(pending); err != nil {
			return m, "", err
		}
	}
	uid, gid := -1, -1
	if m.Role != "client" && runtime.GOOS != "windows" {
		u, err := user.Lookup(m.User)
		if err != nil && runtime.GOOS == "linux" {
			if e = run(ctx, "useradd", "--system", "--home-dir", m.DataDir, "--shell", "/usr/sbin/nologin", m.User); e != nil {
				return m, "", e
			}
			u, err = user.Lookup(m.User)
		}
		if err != nil {
			return m, "", err
		}
		uid, e = strconv.Atoi(u.Uid)
		if e != nil {
			return m, "", e
		}
		if uid <= 0 {
			return m, "", errors.New("service user must have a non-root UID")
		}
		gid, e = strconv.Atoi(u.Gid)
		if e != nil {
			return m, "", e
		}
	}
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		// Root writes nowhere another user could rearrange under it.
		if e = trustedPath(m.InstallDir, installers(), -1); e != nil {
			return m, "", e
		}
		if m.Role != "client" {
			if e = trustedPath(m.DataDir, installers(), uid); e != nil {
				return m, "", e
			}
		}
	}
	if e = makeInstallDirectory(m.InstallDir, 0755, -1, -1); e != nil {
		return m, "", e
	}
	if runtime.GOOS == "windows" {
		if e = restrictWindowsDirectory(ctx, m.InstallDir); e != nil {
			return m, "", e
		}
	}
	if e = trustedRollbackPath(ctx, m.InstallDir, false); e != nil {
		return m, "", e
	}
	unlockInstall, err := secure.Lock(filepath.Join(m.InstallDir, "install.lock"))
	if err != nil {
		return m, "", err
	}
	defer unlockInstall()
	backups := filepath.Join(m.InstallDir, ".backups")
	if e = secure.MkdirAll(backups, 0700); e != nil {
		return m, "", e
	}
	key, e := rollbackKey(ctx, m.InstallDir, true)
	if e != nil {
		return m, "", e
	}
	backup, e := os.MkdirTemp(backups, "install-")
	if e != nil {
		return m, "", e
	}
	record := installation{Manifest: m, WasActive: serviceActive(ctx, m), WasEnabled: serviceEnabled(ctx, m)}
	if runtime.GOOS == "windows" && m.Role != "client" {
		record.TaskXML, e = exportWindowsTask(ctx, m)
		if e != nil {
			return m, backup, e
		}
	}
	targets := []string{}
	for _, f := range m.Files {
		targets = append(targets, targetPath(m.InstallDir, f.Name))
	}
	if m.ServiceFile != "" {
		targets = append(targets, m.ServiceFile)
	}
	for i, target := range targets {
		old := savedFile{Target: target, UID: -1, GID: -1}
		info, err := os.Lstat(target)
		if err == nil {
			if !info.Mode().IsRegular() {
				return m, backup, errors.New("refuses replacing a non-regular installation file")
			}
			b, err := secure.ReadBounded(target, 64<<20)
			if err != nil {
				return m, backup, err
			}
			old.Saved = strconv.Itoa(i)
			old.Mode = uint32(info.Mode().Perm())
			old.UID, old.GID = fileOwner(info)
			old.SHA256 = secure.Hash(string(b))
			if err = secure.AtomicWrite(filepath.Join(backup, old.Saved), b, 0600); err != nil {
				return m, backup, err
			}
		} else if !os.IsNotExist(err) {
			return m, backup, err
		}
		record.Files = append(record.Files, old)
	}
	record.Signature, e = signRollback(record, backup, key)
	if e != nil {
		return m, backup, e
	}
	raw, _ := json.Marshal(record)
	if e = secure.AtomicWrite(filepath.Join(backup, "rollback.json"), raw, 0600); e != nil {
		return m, backup, e
	}
	fail := func(err error) (Manifest, string, error) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if re := rollback(cleanup, backup, true); re != nil {
			err = fmt.Errorf("installation failed: %w; rollback also failed: %v", err, re)
		}
		return m, backup, err
	}
	if e = stopService(ctx, m); e != nil {
		return fail(e)
	}
	for _, f := range m.Files {
		target := targetPath(m.InstallDir, f.Name)
		if err := secure.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fail(err)
		}
		if err := secure.AtomicWrite(target, bundle.files[f.Name], os.FileMode(f.Mode)); err != nil {
			return fail(err)
		}
		if f.Private && uid >= 0 {
			if err := lchown(target, uid, gid); err != nil {
				return fail(err)
			}
		}
	}
	if runtime.GOOS == "windows" {
		if e = restrictWindowsDirectory(ctx, m.InstallDir); e != nil {
			return fail(e)
		}
	}
	if m.Role != "client" {
		// Recheck after installing payloads, before creating or handing over
		// service data. Every existing component must still be trusted.
		if runtime.GOOS != "windows" && os.Geteuid() == 0 {
			if e = trustedPath(m.DataDir, installers(), uid); e != nil {
				return fail(e)
			}
		}
		// A link in the data directory's place would have root make or
		// hand over a directory elsewhere.
		info, err := os.Lstat(m.DataDir)
		switch {
		case err == nil && !info.IsDir():
			return fail(errors.New("data directory is not a directory"))
		case err == nil && uid >= 0:
			if owner, _ := fileOwner(info); owner != uid {
				return fail(errors.New("existing data directory belongs to another service identity"))
			}
		case err != nil && !os.IsNotExist(err):
			return fail(err)
		}
		if e = prepareDataDirectory(ctx, m.DataDir, uid, gid); e != nil {
			return fail(e)
		}
		// The service makes its own lock file; here it is only looked at.
		if busy, err := writerBusy(filepath.Join(m.DataDir, "writer.lock")); err != nil || busy {
			if err == nil {
				err = errors.New("data directory already has an active writer")
			}
			return fail(err)
		}
		if m.ServiceFile != "" {
			if e = secure.AtomicWrite(m.ServiceFile, bundle.service, 0644); e != nil {
				return fail(e)
			}
		}
		if runtime.GOOS == "windows" {
			if e = windowsDirectoryACL(ctx, m.DataDir, "verify"); e != nil {
				return fail(e)
			}
		}
		if e = startService(ctx, m, bundle.service); e != nil {
			return fail(e)
		}
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for !serviceActive(ctx, m) {
			select {
			case <-ctx.Done():
				return fail(ctx.Err())
			case <-deadline.C:
				return fail(errors.New("installed service did not remain active"))
			case <-tick.C:
			}
		}
	}
	return m, backup, nil
}

func checkPendingOutbox(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat existing worker outbox: %w", err)
	}
	if !info.IsDir() {
		return errors.New("existing worker outbox must be a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read existing worker outbox: %w", err)
	}
	if len(entries) > 0 {
		return errors.New("drain or archive the existing worker outbox before installation")
	}
	return nil
}

func prepareDataDirectory(ctx context.Context, path string, uid, gid int) error {
	if err := makeInstallDirectory(path, 0700, uid, gid); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// An external DataDir does not inherit InstallDir's private ACL.
		return restrictWindowsDirectory(ctx, path)
	}
	return nil
}
func Rollback(ctx context.Context, dir string) error { return rollback(ctx, dir, false) }
func rollback(ctx context.Context, dir string, alreadyLocked bool) error {
	r, payloads, e := loadRollback(ctx, dir)
	if e != nil {
		return e
	}
	if !alreadyLocked {
		unlock, e := secure.Lock(filepath.Join(r.Manifest.InstallDir, "install.lock"))
		if e != nil {
			return e
		}
		defer unlock()
	}
	if r.Manifest.Platform != runtime.GOOS {
		return errors.New("rollback platform mismatch")
	}
	if r.Manifest.Role != "client" && runtime.GOOS != "windows" && os.Geteuid() != 0 {
		return errors.New("rollback requires administrator")
	}
	if e = stopService(ctx, r.Manifest); e != nil {
		return e
	}
	for _, f := range r.Files {
		if f.Saved == "" {
			if e = os.Remove(f.Target); e != nil && !os.IsNotExist(e) {
				return e
			}
			continue
		}
		if e = secure.AtomicWrite(f.Target, payloads[f.Saved], os.FileMode(f.Mode)); e != nil {
			return e
		}
		if f.UID >= 0 && runtime.GOOS != "windows" {
			if e = lchown(f.Target, f.UID, f.GID); e != nil {
				return e
			}
		}
	}
	return restoreService(ctx, r)
}
func run(ctx context.Context, name string, args ...string) error {
	return runWithTimeout(ctx, 15*time.Second, name, args...)
}

func runWithTimeout(ctx context.Context, budget time.Duration, name string, args ...string) error {
	sub, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := exec.CommandContext(sub, name, args...).Run(); err != nil {
		if sub.Err() != nil {
			err = sub.Err()
		}
		return fmt.Errorf("%s operation failed: %w", name, err)
	}
	return nil
}
