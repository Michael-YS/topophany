//go:build !windows && !js

package outbox

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOutboxRejectsNonRegularEntries(t *testing.T) {
	for _, sub := range []string{"pending", "quarantine"} {
		for _, kind := range []string{"symlink", "fifo", "directory"} {
			t.Run(sub+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, sub, "record.json")
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
				if q, err := Open(dir, "lab", 1<<20); err == nil {
					q.Close()
					t.Fatal("non-regular outbox entry accepted")
				}
			})
		}
	}
}
