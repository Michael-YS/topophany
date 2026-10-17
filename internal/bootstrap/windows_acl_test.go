//go:build windows

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsInstallationReplacesExplicitACLs(t *testing.T) {
	ctx := context.Background()
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "upgrade"}[existing], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "installation [private] with ' quotes")
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			if existing {
				if err := os.WriteFile(filepath.Join(bin, "topo.exe"), []byte("old binary"), 0600); err != nil {
					t.Fatal(err)
				}
				// Both the root and a child carry unrelated explicit grants.
				for _, path := range []string{dir, bin} {
					if err := run(ctx, "icacls.exe", path, "/grant", "*S-1-5-32-545:(OI)(CI)M"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := restrictWindowsDirectory(ctx, dir); err != nil {
				t.Fatal(err)
			}
			// Reapplying the DACL must also work without audit privileges.
			if err := restrictWindowsDirectory(ctx, dir); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"credentials.json", "server-key.pem", "topology.json", "bin/topo.exe"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic private payload"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Inspect the actual ACLs before the post-install restriction, so
			// newly created files must already have inherited private grants.
			if err := windowsDirectoryACL(ctx, dir, "verify"); err != nil {
				t.Fatal(err)
			}
			if err := restrictWindowsDirectory(ctx, dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsACLVerificationRejectsExplicitGrant(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	if err := restrictWindowsDirectory(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, "icacls.exe", dir, "/grant", "*S-1-5-32-545:(OI)(CI)R"); err != nil {
		t.Fatal(err)
	}
	if err := windowsDirectoryACL(ctx, dir, "verify"); err == nil {
		t.Fatal("unrelated explicit read access accepted")
	}
}
