package secure

import (
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
