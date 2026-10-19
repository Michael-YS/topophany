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

func TestWindowsACLDoesNotRequireSecurityModule(t *testing.T) {
	// A Windows PowerShell subprocess can inherit an incompatible security
	// module from a PowerShell 7 parent, as it does on GitHub's Windows runner.
	modules := t.TempDir()
	module := filepath.Join(modules, "Microsoft.PowerShell.Security")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"Microsoft.PowerShell.Security.psd1": "@{ RootModule='Microsoft.PowerShell.Security.psm1'; ModuleVersion='99.0'; FunctionsToExport=@('Get-Acl','Set-Acl') }",
		"Microsoft.PowerShell.Security.psm1": "function Get-Acl { throw 'Incompatible security module' }; function Set-Acl { throw 'Incompatible security module' }",
	} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PSModulePath", modules+string(os.PathListSeparator)+os.Getenv("PSModulePath"))
	ctx, dir := context.Background(), t.TempDir()
	if err := restrictWindowsDirectory(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := windowsDirectoryACL(ctx, dir, "verify"); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsExternalDataDirectoryIsPrivateBeforeStateAccess(t *testing.T) {
	ctx := context.Background()
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "existing"}[existing], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "external-data")
			if existing {
				if err := os.MkdirAll(filepath.Join(dir, "pending"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "pending", "record"), []byte("private state"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := run(ctx, "icacls.exe", dir, "/grant", "*S-1-5-32-545:(OI)(CI)M", "/T"); err != nil {
					t.Fatal(err)
				}
			}
			if err := prepareDataDirectory(ctx, dir, -1, -1); err != nil {
				t.Fatal(err)
			}
			if err := windowsDirectoryACL(ctx, dir, "verify"); err != nil {
				t.Fatal("external state remained accessible to unrelated users", err)
			}
		})
	}
}

func TestWindowsDataDirectoryRejectsReparsePoints(t *testing.T) {
	ctx := context.Background()
	dir, target := t.TempDir(), t.TempDir()
	link := filepath.Join(dir, "redirect")
	if err := run(ctx, "cmd.exe", "/c", "mklink", "/J", link, target); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(link)
	if err := prepareDataDirectory(ctx, filepath.Join(link, "state"), -1, -1); err == nil {
		t.Fatal("reparse ancestor accepted")
	}
	if err := prepareDataDirectory(ctx, dir, -1, -1); err == nil {
		t.Fatal("reparse child accepted")
	}
	if _, err := os.Stat(filepath.Join(target, "state")); !os.IsNotExist(err) {
		t.Fatal("reparse target was changed", err)
	}
}
