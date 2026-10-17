//go:build !windows

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRollbackRejectsWritableAndSymlinkedDirectories(t *testing.T) {
	_, backup, target := installedBackup(t)
	before, _ := os.ReadFile(target)
	if err := os.Chmod(backup, 0777); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(context.Background(), backup); err == nil {
		t.Fatal("writable backup accepted")
	}
	if err := os.Chmod(backup, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(backup), "install-link")
	if err := os.Symlink(backup, link); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(context.Background(), link); err == nil {
		t.Fatal("symlinked backup accepted")
	}
	after, _ := os.ReadFile(target)
	if string(after) != string(before) {
		t.Fatal("rejected backup changed installed files")
	}
}

func TestPrivilegedRollbackRejectsForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create a file owned by a different UID")
	}
	_, backup, _ := installedBackup(t)
	if err := os.Chown(backup, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	defer os.Chown(backup, 0, 0)
	if err := Rollback(context.Background(), backup); err == nil {
		t.Fatal("foreign-owned private backup accepted by root")
	}
}
