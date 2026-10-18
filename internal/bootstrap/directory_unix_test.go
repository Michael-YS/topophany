//go:build !windows

package bootstrap

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestTrustedDirectoriesRejectSymlinksBeforeCreatingAnything(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Join(link, "missing", "data")} {
		if err := makeTrustedDirectory(path, 0700, []int{0, os.Geteuid()}, -1, -1); err == nil {
			t.Fatal("accepted symlink", path)
		}
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatal("rejection changed symlink target", entries, err)
	}
	safe := filepath.Join(base, "safe", "nested")
	if err := makeTrustedDirectory(safe, 0700, []int{0, os.Geteuid()}, -1, -1); err != nil {
		t.Fatal("safe nested path rejected", err)
	}
	if err := makeTrustedDirectory(safe, 0700, []int{0, os.Geteuid()}, -1, -1); err != nil {
		t.Fatal("existing safe path rejected", err)
	}
}

func TestPrivilegedInstallRejectsIntermediateLinksBeforeMutation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root for privileged installation")
	}
	for _, role := range []string{"client", "worker"} {
		t.Run(role, func(t *testing.T) {
			bundle, opts := clientBundle(t)
			if role == "worker" {
				u, err := user.LookupId("65534")
				if err != nil {
					t.Skip("no existing unprivileged service identity")
				}
				opts.Role, opts.Observer, opts.User = role, "home", u.Username
			}
			target := t.TempDir()
			before, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if role == "client" {
				opts.InstallDir = filepath.Join(link, "new", "install")
			} else {
				opts.DataDir = filepath.Join(link, "new", "data")
			}
			bundle = filepath.Join(t.TempDir(), "bundle")
			if _, err := Generate(bundle, opts); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Install(context.Background(), bundle, false); err == nil {
				t.Fatal("root installation accepted intermediate symlink")
			}
			after, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(target)
			beforeUID, beforeGID := fileOwner(before)
			afterUID, afterGID := fileOwner(after)
			if err != nil || len(entries) != 0 || beforeUID != afterUID || beforeGID != afterGID {
				t.Fatal("rejected install mutated the target", entries, err)
			}
			if _, err := os.Stat(opts.InstallDir); !os.IsNotExist(err) {
				t.Fatal("rejected install created its installation directory", err)
			}
		})
	}
}

func TestDirectoryFailureRemovesOnlyNewEmptyDirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an unprivileged caller to exercise a failing chown")
	}
	base := t.TempDir()
	marker := filepath.Join(base, "keep")
	if err := os.WriteFile(marker, []byte("existing data"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "new", "nested")
	if err := makeTrustedDirectory(path, 0700, []int{0, os.Geteuid()}, 0, 0); err == nil {
		t.Fatal("unprivileged chown to root succeeded")
	}
	if _, err := os.Stat(filepath.Join(base, "new")); !os.IsNotExist(err) {
		t.Fatal("failed creation left new directories behind", err)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "existing data" {
		t.Fatal("cleanup changed existing data", err)
	}
}
