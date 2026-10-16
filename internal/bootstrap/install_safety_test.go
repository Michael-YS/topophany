//go:build !windows

package bootstrap

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// systemd takes WorkingDirectory= and EnvironmentFile= as written and
// refuses a unit whose path there is quoted, so a bundle's unit must write
// them bare (with specifiers escaped), and the unit must pass systemd's own
// check where systemd is installed.
func TestSystemdUnitPathsAreUnquoted(t *testing.T) {
	o := options(t, "worker")
	o.Platform = "linux"
	o.Observer = "home"
	o.InstallDir = filepath.Join(t.TempDir(), "opt", "topo phany")
	o.DataDir = filepath.Join(t.TempDir(), "var", "lib", "topophany 100%")
	_, _, raw, e := service(o)
	if e != nil {
		t.Fatal(e)
	}
	unit := string(raw)
	for _, want := range []string{
		"WorkingDirectory=" + strings.ReplaceAll(o.DataDir, "%", "%%") + "\n",
		"EnvironmentFile=-" + o.InstallDir + "/worker.env\n",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit lacks %q:\n%s", want, unit)
		}
	}
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("no systemd-analyze to check the unit with")
	}
	// The unit names an executable systemd checks for.
	exe := filepath.Join(o.InstallDir, "bin", "topo")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	if e := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "topophany-worker.service")
	os.WriteFile(file, raw, 0o644)
	out, _ := exec.Command(analyze, "verify", file).CombinedOutput()
	for _, bad := range []string{"not absolute", "bad unit file setting", "fatal error"} {
		if strings.Contains(string(out), bad) {
			t.Fatalf("systemd refuses the unit:\n%s\n%s", out, unit)
		}
	}
}

// What is installed is what was checked: a payload changed in the bundle
// after its checksum matched never reaches the installation.
func TestInstallWritesTheBytesItVerified(t *testing.T) {
	dir, o := clientBundle(t)
	payload := filepath.Join(dir, "payload", "credentials.json")
	reviewed, e := os.ReadFile(payload)
	if e != nil {
		t.Fatal(e)
	}
	checked = func(string) { os.WriteFile(payload, []byte("swapped"), 0o600) }
	defer func() { checked = nil }()
	if _, _, e := Install(context.Background(), dir, false); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(o.InstallDir, "credentials.json")); string(b) != string(reviewed) {
		t.Fatalf("installed %q, not the reviewed payload", b)
	}
}

func TestWriterBusyNeverFollowsOrCreates(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "writer.lock")
	if busy, e := writerBusy(lock); busy || e != nil {
		t.Fatal(busy, e)
	}
	if _, e := os.Stat(lock); !os.IsNotExist(e) {
		t.Fatal("probing the lock created it")
	}
	// A link in the lock's place is refused, and its target left alone.
	target := filepath.Join(dir, "elsewhere")
	if e := os.Symlink(target, lock); e != nil {
		t.Fatal(e)
	}
	if _, e := writerBusy(lock); e == nil {
		t.Fatal("followed a symbolic link")
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("created the link's target")
	}
	os.Remove(lock)
	f, _ := os.Create(lock)
	defer f.Close()
	if busy, e := writerBusy(lock); busy || e != nil {
		t.Fatal("an unheld lock reads busy", e)
	}
	if e := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	if busy, e := writerBusy(lock); !busy || e != nil {
		t.Fatal("a held lock reads free", e)
	}
}

func TestTrustedPathRefusesOthersDirectories(t *testing.T) {
	me := os.Getuid()
	base := t.TempDir()
	os.Chmod(base, 0o755)
	if e := trustedPath(filepath.Join(base, "new", "dir"), []int{0, me}, -1); e != nil {
		t.Fatal("a path under the installer's own directory refused:", e)
	}
	open := filepath.Join(base, "open")
	os.Mkdir(open, 0o777)
	os.Chmod(open, 0o777)
	if e := trustedPath(filepath.Join(open, "topophany"), []int{0, me}, -1); e == nil {
		t.Fatal("a world-writable parent accepted")
	}
	if e := trustedPath(base, []int{0}, -1); e == nil && me != 0 {
		t.Fatal("a directory owned by another user accepted")
	}
	// The service may own its data directory, not the directories above it.
	if e := trustedPath(filepath.Join(base, "data"), []int{0}, me); e == nil && me != 0 {
		t.Fatal("a directory above the data directory, owned by the service, accepted")
	}
}

func TestPrivateAuthorityCannotSignAuthorities(t *testing.T) {
	ca, _, _, _, e := certificates([]string{"localhost"})
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode(ca)
	c, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	if !c.IsCA || c.MaxPathLen != 0 || !c.MaxPathLenZero {
		t.Fatal("the private CA may sign intermediate authorities")
	}
}
