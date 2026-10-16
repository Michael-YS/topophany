package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRotationBoundsRetentionAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topophany.log")
	w, e := Open(path, 128, 3)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := Open(path, 128, 3); e == nil {
		other.Close()
		t.Fatal("second log writer")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, e := w.Write(bytes.Repeat([]byte("x"), 200)); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if _, e = w.Write([]byte("last-record")); e != nil {
		t.Fatal(e)
	}
	w.Close()
	entries, e := os.ReadDir(filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) > 5 {
		t.Fatal("unbounded backups", entries)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		st, _ := entry.Info()
		if st.Size() > 128 {
			t.Fatal("oversized log", st)
		}
	}
	b, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(b), "last-record") {
		t.Fatal("writer kept old handle", string(b))
	}
	w, e = Open(path, 128, 3)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if _, e = w.Write([]byte("restart")); e != nil {
		t.Fatal(e)
	}
}
func TestRepeatedErrorSummaryRecoveryAndBound(t *testing.T) {
	var b bytes.Buffer
	r := Repeater{Out: &b, Every: time.Minute}
	now := time.Now()
	for i := 0; i < 60; i++ {
		r.Update("delivery", "fixture", "unreachable", now.Add(time.Duration(i)*time.Second))
	}
	if strings.Count(b.String(), "event=") != 1 {
		t.Fatal(b.String())
	}
	r.Update("delivery", "fixture", "unreachable", now.Add(time.Minute))
	r.Update("delivery", "fixture", "", now.Add(61*time.Second))
	if !strings.Contains(b.String(), "event=repeated") || !strings.Contains(b.String(), "event=recovered") || !strings.Contains(b.String(), "reports=61") {
		t.Fatal(b.String())
	}
	for i := 0; i < 200; i++ {
		r.Update("fixture", string(rune(0x100+i)), "error", now)
	}
	if len(r.entries) != 128 {
		t.Fatal("unbounded error groups")
	}
}
