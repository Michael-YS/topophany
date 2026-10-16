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
	"strconv"
	"time"
)

type savedFile struct {
	Target string `json:"target"`
	Saved  string `json:"saved,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
}
type installation struct {
	TaskXML string `json:"task_xml,omitempty"`

	Manifest   Manifest    `json:"manifest"`
	Files      []savedFile `json:"files"`
	WasActive  bool        `json:"was_active"`
	WasEnabled bool        `json:"was_enabled"`
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

func Install(ctx context.Context, dir string, dry bool) (Manifest, string, error) {
	m, bundle, e := loadBundle(dir)
	if e != nil {
		return m, "", e
	}
	if dry {
		return m, "", nil
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
		entries, _ := os.ReadDir(pending)
		if len(entries) > 0 {
			return m, "", errors.New("drain or archive the existing worker outbox before installation")
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
	if e = secure.MkdirAll(m.InstallDir, 0755); e != nil {
		return m, "", e
	}
	unlockInstall, err := secure.Lock(filepath.Join(m.InstallDir, "install.lock"))
	if err != nil {
		return m, "", err
	}
	defer unlockInstall()
	if runtime.GOOS == "windows" {
		if e = restrictWindowsDirectory(ctx, m.InstallDir); e != nil {
			return m, "", e
		}
	}
	backups := filepath.Join(m.InstallDir, ".backups")
	if e = secure.MkdirAll(backups, 0700); e != nil {
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
			if err = secure.AtomicWrite(filepath.Join(backup, old.Saved), b, 0600); err != nil {
				return m, backup, err
			}
		} else if !os.IsNotExist(err) {
			return m, backup, err
		}
		record.Files = append(record.Files, old)
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
		if e = secure.MkdirAll(m.DataDir, 0700); e != nil {
			return fail(e)
		}
		if uid >= 0 {
			if e = lchown(m.DataDir, uid, gid); e != nil {
				return fail(e)
			}
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
func Rollback(ctx context.Context, dir string) error { return rollback(ctx, dir, false) }
func rollback(ctx context.Context, dir string, alreadyLocked bool) error {
	var r installation
	b, e := secure.PrivateRead(filepath.Join(dir, "rollback.json"))
	if e != nil {
		return e
	}
	if e = secure.Decode(bytes.NewReader(b), &r, 1<<20); e != nil {
		return e
	}
	if e = validateRollback(r); e != nil {
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
	allowed := map[string]bool{}
	for _, file := range r.Manifest.Files {
		allowed[targetPath(r.Manifest.InstallDir, file.Name)] = true
	}
	if r.Manifest.ServiceFile != "" {
		allowed[r.Manifest.ServiceFile] = true
	}
	for i, f := range r.Files {
		if !allowed[f.Target] || f.Saved != "" && f.Saved != strconv.Itoa(i) || f.Mode > 0777 {
			return errors.New("invalid rollback file record")
		}
	}
	for _, f := range r.Files {
		if f.Saved == "" {
			if e = os.Remove(f.Target); e != nil && !os.IsNotExist(e) {
				return e
			}
			continue
		}
		b, e = secure.PrivateRead(filepath.Join(dir, f.Saved))
		if e != nil {
			return e
		}
		if e = secure.AtomicWrite(f.Target, b, os.FileMode(f.Mode)); e != nil {
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
	sub, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(sub, name, args...).Run(); err != nil {
		return fmt.Errorf("%s operation failed: %w", name, err)
	}
	return nil
}
