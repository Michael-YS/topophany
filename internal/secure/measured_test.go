package secure

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
)

func TestMeasuredDurableWriteAndUnsyncedCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "durable.json")
	payload := []byte(`{"fixture":true}`)
	var counter ioaccount.Counter
	before := ioaccount.FileSync.Read().Count
	if e := AtomicWriteMeasured(path, payload, 0600, &counter); e != nil {
		t.Fatal(e)
	}
	if v := counter.Read(); v.Count != 1 || v.Bytes != uint64(len(payload)) || v.Errors != 0 {
		t.Fatal(v)
	}
	if ioaccount.FileSync.Read().Count <= before {
		t.Fatal("durable write lost file sync")
	}
	before = ioaccount.FileSync.Read().Count
	if e := CacheWrite(filepath.Join(dir, "cache.json"), payload, 0600); e != nil {
		t.Fatal(e)
	}
	if ioaccount.FileSync.Read().Count != before {
		t.Fatal("cache forced file sync")
	}
	blocked := filepath.Join(dir, "blocked.json")
	os.Mkdir(blocked, 0700)
	if e := AtomicWriteMeasured(blocked, payload, 0600, &counter); e == nil {
		t.Fatal("directory overwrite accepted")
	}
	if v := counter.Read(); v.Count != 2 || v.Bytes != uint64(len(payload)) || v.Errors != 1 {
		t.Fatal(v)
	}
}

func TestStreamingFailureNeverPublishesPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")
	if err := AtomicWrite(path, []byte("old state"), 0600); err != nil {
		t.Fatal(err)
	}
	var counter ioaccount.Counter
	err := AtomicWriteStreamMeasured(path, 0600, &counter, func(w io.WriteSeeker) error {
		if _, err := io.WriteString(w, "partial state"); err != nil {
			return err
		}
		return errors.New("injected encoding failure")
	})
	if err == nil {
		t.Fatal("failed producer accepted")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "old state" {
		t.Fatal("partial state replaced durable checkpoint")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary file left behind")
	}
	if v := counter.Read(); v.Errors != 1 || v.Bytes != uint64(len("partial state")) {
		t.Fatal(v)
	}
}
