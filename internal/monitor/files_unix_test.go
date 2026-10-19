//go:build !windows && !js

package monitor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOutputRejectsNonRegularFilesAndParentAliases(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "metrics.json")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(filepath.Join(t.TempDir(), "absent"), path)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := PrepareOutput(path, "desktop"); err == nil {
				t.Fatal("non-regular output accepted")
			}
		})
	}
	protected := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	parentAlias := filepath.Join(t.TempDir(), "parent-alias")
	if err := os.Symlink(filepath.Dir(protected), parentAlias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(alias, "metrics.json")
	for _, p := range []string{protected, filepath.Join(protected, "metrics.json"), filepath.Join(parentAlias, filepath.Base(protected), "metrics.json")} {
		if _, err := PrepareOutput(path, "desktop", p); err == nil {
			t.Fatalf("aliased protected output accepted: %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(protected, "metrics.json")); !os.IsNotExist(err) {
		t.Fatal("alias rejection created output", err)
	}
}
