package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func installedBackup(t *testing.T) (string, string, string) {
	t.Helper()
	bundle, o := clientBundle(t)
	if err := os.MkdirAll(o.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(o.InstallDir, "credentials.json")
	if err := os.WriteFile(target, []byte("old credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	_, backup, err := Install(context.Background(), bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	return o.InstallDir, backup, target
}

func TestRollbackAuthenticatesMetadataAndEveryPayload(t *testing.T) {
	for _, kind := range []string{"metadata", "payload", "unsigned", "key", "missing payload"} {
		t.Run(kind, func(t *testing.T) {
			install, backup, target := installedBackup(t)
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(backup, "rollback.json")
			raw, _ := os.ReadFile(path)
			var record installation
			if err = json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "metadata":
				record.WasActive = !record.WasActive
			case "unsigned":
				record.Signature = ""
			case "key":
				err = os.WriteFile(filepath.Join(install, ".backups", "rollback.key"), bytes.Repeat([]byte{1}, 32), 0600)
			case "payload", "missing payload":
				for _, f := range record.Files {
					if f.Saved != "" {
						path := filepath.Join(backup, f.Saved)
						if kind == "payload" {
							err = os.WriteFile(path, []byte("attacker supplied service or credentials"), 0600)
						} else {
							err = os.Remove(path)
						}
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ = json.Marshal(record)
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err = Rollback(context.Background(), backup); err == nil {
				t.Fatal("unauthenticated rollback accepted")
			}
			after, _ := os.ReadFile(target)
			if !bytes.Equal(before, after) {
				t.Fatal("target changed before complete backup verification")
			}
		})
	}
}

func TestRollbackRejectsCopiedBackupAndInstallation(t *testing.T) {
	install, backup, _ := installedBackup(t)
	for _, copyTree := range []bool{false, true} {
		dest := filepath.Join(t.TempDir(), "copy")
		source := backup
		selected := dest
		if copyTree {
			source = install
			selected = filepath.Join(dest, ".backups", filepath.Base(backup))
		}
		if err := os.CopyFS(dest, os.DirFS(source)); err != nil {
			t.Fatal(err)
		}
		// CopyFS gives files public modes; use private modes so that path
		// binding, rather than just a permission failure, rejects the copy.
		if err := filepath.WalkDir(dest, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if d.IsDir() {
				mode = 0700
			}
			return os.Chmod(path, mode)
		}); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" {
			if err := restrictWindowsDirectory(context.Background(), dest); err != nil {
				t.Fatal(err)
			}
		}
		if err := Rollback(context.Background(), selected); err == nil {
			t.Fatal("backup copied out of its installation accepted")
		}
	}
}

func TestRollbackRestoresLargeAuthenticatedExecutable(t *testing.T) {
	bundle, o := clientBundle(t)
	name := "topo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(o.InstallDir, "bin", name)
	old := bytes.Repeat([]byte("synthetic executable"), 100000)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, old, 0755); err != nil {
		t.Fatal(err)
	}
	_, backup, err := Install(context.Background(), bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = Rollback(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatal("authenticated executable over 1 MiB not restored", err)
	}
}
