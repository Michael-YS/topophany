package logging

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagedLogsRejectPermissiveFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows access is controlled by deployment ACLs")
	}
	for _, suffix := range []string{"", ".1", ".2"} {
		t.Run("existing"+suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "service.log")
			unsafe := path + suffix
			if e := os.WriteFile(unsafe, []byte("existing metadata"), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.Chmod(unsafe, 0644); e != nil {
				t.Fatal(e)
			}
			if w, e := Open(path, 32, 2); e == nil {
				w.Close()
				t.Fatal("accepted a group/world-readable log or backup")
			}
			if b, e := os.ReadFile(unsafe); e != nil || string(b) != "existing metadata" {
				t.Fatal("refusal changed the existing file", e)
			}
			if e := os.Chmod(unsafe, 0600); e != nil {
				t.Fatal(e)
			}
			w, e := Open(path, 32, 2)
			if e != nil {
				t.Fatal("refusal retained the writer lock", e)
			}
			defer w.Close()
			if _, e = w.Write([]byte(strings.Repeat("x", 96))); e != nil {
				t.Fatal(e)
			}
			for _, suffix := range []string{"", ".1", ".2"} {
				st, e := os.Stat(path + suffix)
				if e != nil || st.Mode().Perm()&0077 != 0 {
					t.Fatal("rotation exposed a log", suffix, st, e)
				}
			}
		})
	}
}
