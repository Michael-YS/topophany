//go:build !windows && !js

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestJournalRejectsNonRegularFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "journal.jsonl")
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, path)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if s, err := Open(dir, Options{}); err == nil {
				s.Close()
				t.Fatal("non-regular journal accepted")
			}
			if raw, err := os.ReadFile(target); err != nil || string(raw) != "unchanged" {
				t.Fatal("rejected journal changed target", err)
			}
		})
	}
}
