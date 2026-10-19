//go:build !windows

package bootstrap

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func TestPrivilegedBundleSourcePolicy(t *testing.T) {
	for _, kind := range []string{"safe", "group writable", "world writable", "symlink", "directory", "missing", "parent symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			payload := filepath.Join(dir, "payload")
			if err := os.Mkdir(payload, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"manifest.json", "payload/topo", "service"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("reviewed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(payload, "topo")
			switch kind {
			case "group writable", "world writable":
				mode := os.FileMode(0660)
				if kind == "world writable" {
					mode = 0666
				}
				if err := os.Chmod(file, mode); err != nil {
					t.Fatal(err)
				}
			case "symlink", "directory", "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink(filepath.Join(dir, "manifest.json"), file); err != nil {
						t.Fatal(err)
					}
				} else if kind == "directory" {
					if err := os.Mkdir(file, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "parent symlink":
				target := filepath.Join(dir, "original")
				if err := os.Rename(payload, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, payload); err != nil {
					t.Fatal(err)
				}
			}
			m := Manifest{Role: "worker", Files: []File{{Source: "payload/topo"}}}
			err := trustedBundleFiles(dir, m, []int{0, os.Geteuid()})
			if (err == nil) != (kind == "safe") {
				t.Fatalf("source policy: %v", err)
			}
			if kind == "safe" && os.Geteuid() != 0 {
				if err := trustedBundleFiles(dir, m, []int{0}); err == nil {
					t.Fatal("untrusted owner accepted")
				}
			}
		})
	}
}

func TestInstallerIdentities(t *testing.T) {
	for _, value := range []string{"", "invalid", "-1", "0", strconv.Itoa(os.Geteuid()), "12345"} {
		t.Setenv("SUDO_UID", value)
		got := installers()
		id, err := strconv.Atoi(value)
		if len(got) == 0 || got[0] != 0 || (len(got) == 2) != (err == nil && id > 0) || err == nil && id > 0 && !slices.Contains(got, id) {
			t.Fatalf("SUDO_UID=%q: %v", value, got)
		}
	}
}
