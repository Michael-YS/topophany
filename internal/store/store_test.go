package store

import (
	"encoding/json"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func batch(c model.Config, v ...model.Observation) model.Batch {
	return model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: v}
}
func openTest(t *testing.T, dir string, o Options) *Store {
	t.Helper()
	s, e := Open(dir, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestDurableDetachedDedupeAndLateReplay(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	opts := Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Now: func() time.Time { return now }}
	s := openTest(t, dir, opts)
	newer := testkit.Observation(c, "home-http", 2, now.Add(-time.Second), "ok")
	older := testkit.Observation(c, "home-http", 1, now.Add(-time.Minute), "fail")
	if e := s.Put(batch(c, newer)); e != nil {
		t.Fatal(e)
	}
	newer.Facts["response_proof"] = "mutated-by-caller"
	if e := s.Put(batch(c, older)); e != nil {
		t.Fatal(e)
	}
	if e := s.Put(batch(c, older)); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.View()
	if len(rows) != 2 {
		t.Fatal("duplicate observation stored", len(rows))
	}
	rows[0].Facts["test"] = "mutate view"
	rows2, _ := s.View()
	if rows2[0].Facts["test"] != "" {
		t.Fatal("view shares persistence maps")
	}
	if rows2[1].Facts["response_proof"] == "mutated-by-caller" {
		t.Fatal("Put borrowed caller map")
	}
	if !rows2[0].SampledAt.Equal(older.SampledAt) || !rows2[0].ReceivedAt.Equal(now) {
		t.Fatal("sample/receipt clocks confused")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, dir, opts)
	rows, beats := s.View()
	snap := engine.Build(c, "a", "home", rows, beats, now)
	if snap.Checks[0].Last.Sequence != 2 || snap.Checks[0].Last.Outcome != "ok" {
		t.Fatal("backfill resurrected old outcome")
	}
}
func TestBatchConflictIsAtomic(t *testing.T) {
	c := testkit.Config()
	s := openTest(t, t.TempDir(), Options{})
	v := testkit.Observation(c, "home-http", 1, time.Now(), "ok")
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal(e)
	}
	changed := v
	changed.Outcome = "fail"
	newV := testkit.Observation(c, "home-http", 2, time.Now(), "ok")
	if e := s.Put(batch(c, newV, changed)); !errors.Is(e, ErrConflict) {
		t.Fatal("ID conflict not rejected", e)
	}
	rows, _ := s.View()
	if len(rows) != 1 {
		t.Fatal("partially applied invalid batch")
	}
	if e := s.Put(batch(c, newV, newV)); e != nil {
		t.Fatal(e)
	}
	rows, _ = s.View()
	if len(rows) != 2 {
		t.Fatal("intra-batch duplicates counted")
	}
}
func TestRecoveryOfOnlyIncompleteTail(t *testing.T) {
	c := testkit.Config()
	dir := t.TempDir()
	s := openTest(t, dir, Options{})
	if e := s.Put(batch(c, testkit.Observation(c, "home-http", 1, time.Now(), "ok"))); e != nil {
		t.Fatal(e)
	}
	s.Close()
	file := filepath.Join(dir, "journal.jsonl")
	original, _ := os.ReadFile(file)
	f, e := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString(`{"sha256":"incomplete`)
	f.Close()
	s = openTest(t, dir, Options{})
	got, _ := os.ReadFile(file)
	if string(got) != string(original) {
		t.Fatal("did not truncate only incomplete final line")
	}
	s.Close()
	if e = os.WriteFile(file, append(original, []byte("{\"sha256\":\"bad\",\"payload\":{}}\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	if bad, e := Open(dir, Options{}); e == nil {
		bad.Close()
		t.Fatal("complete corrupt record silently ignored")
	}
}
func TestCheckpointCrashWindowReplay(t *testing.T) {
	c := testkit.Config()
	dir := t.TempDir()
	s := openTest(t, dir, Options{})
	now := time.Now()
	v := testkit.Observation(c, "home-http", 1, now, "ok")
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal(e)
	}
	wal, _ := os.ReadFile(filepath.Join(dir, "journal.jsonl"))
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if e := os.WriteFile(filepath.Join(dir, "journal.jsonl"), wal, 0600); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, dir, Options{})
	rows, _ := s.View()
	if len(rows) != 1 {
		t.Fatal("checkpoint + original WAL double applied")
	}
	s.Close()
	if e := os.WriteFile(filepath.Join(dir, "checkpoint.json"), []byte(`{"sha256":"bad","payload":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if bad, e := Open(dir, Options{}); e == nil {
		bad.Close()
		t.Fatal("corrupt checkpoint accepted")
	}
}
func TestHistoryBudgetKeepsReceiptsAndState(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	s := openTest(t, t.TempDir(), Options{MaxHistory: 2, Now: func() time.Time { return now }})
	for i := 1; i <= 5; i++ {
		if e := s.Put(batch(c, testkit.Observation(c, "home-http", uint64(i), now.Add(time.Duration(i-6)*time.Second), "fail"))); e != nil {
			t.Fatal(e)
		}
	}
	if n := len(s.History("home-http", config.Contracts(c), config.Revision(c), 500)); n != 2 {
		t.Fatal("history cap failed", n)
	}
	rows, _ := s.View()
	if len(rows) != 5 {
		t.Fatal("state confirmation tail lost")
	}
	if e := s.Put(batch(c, testkit.Observation(c, "home-http", 1, now.Add(-5*time.Second), "fail"))); e != nil {
		t.Fatal(e)
	}
	rows, _ = s.View()
	if len(rows) != 5 {
		t.Fatal("evicted raw-history duplicate reapplied")
	}
}
func TestReceiptCapacityAndHeartbeatMonotonicity(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	s := openTest(t, t.TempDir(), Options{MaxReceipts: 1, Retention: time.Minute, Now: func() time.Time { return now }})
	v := testkit.Observation(c, "home-http", 1, now, "ok")
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal(e)
	}
	if e := s.Put(batch(c, testkit.Observation(c, "home-http", 2, now, "ok"))); !errors.Is(e, ErrCapacity) {
		t.Fatal("capacity did not backpressure", e)
	}
	b := batch(c)
	b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "new", SampledAt: now}
	if e := s.Put(b); e != nil {
		t.Fatal(e)
	}
	b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "old", SampledAt: now.Add(-time.Second)}
	if e := s.Put(b); e != nil {
		t.Fatal(e)
	}
	_, beats := s.View()
	if beats["home"].Boot != "new" {
		t.Fatal("old heartbeat resurrected")
	}
	now = now.Add(2 * time.Minute)
	if e := s.Put(batch(c, testkit.Observation(c, "home-http", 3, now, "ok"))); !errors.Is(e, ErrCapacity) {
		t.Fatal("receipt capacity released inside the ingestion window", e)
	}
}
func TestStorageErrorsFailClosed(t *testing.T) {
	s := openTest(t, t.TempDir(), Options{})
	s.file.Close()
	c := testkit.Config()
	e := s.Put(batch(c, testkit.Observation(c, "home-http", 1, time.Now(), "ok")))
	if e == nil {
		t.Fatal("acknowledged failed journal write")
	}
	if e = s.Put(batch(c, testkit.Observation(c, "home-http", 2, time.Now(), "ok"))); e == nil {
		t.Fatal("poisoned store accepted data")
	}
	s2 := openTest(t, t.TempDir(), Options{})
	if e = os.Mkdir(filepath.Join(s2.dir, "checkpoint.json"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = s2.Checkpoint(); e == nil {
		t.Fatal("checkpoint failure ignored")
	}
	if e = s2.Put(batch(c, testkit.Observation(c, "home-http", 3, time.Now(), "ok"))); e == nil {
		t.Fatal("checkpoint-poisoned store accepted data")
	}
}
func TestConcurrentStoreReadWrite(t *testing.T) {
	c := testkit.Config()
	s := openTest(t, t.TempDir(), Options{})
	var wg sync.WaitGroup
	errch := make(chan error, 32)
	for i := 1; i <= 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := testkit.Observation(c, "home-http", uint64(i), time.Now(), "ok")
			errch <- s.Put(batch(c, v))
			rows, _ := s.View()
			if len(rows) > 0 {
				rows[0].Facts["local"] = "not shared"
			}
		}(i)
	}
	wg.Wait()
	close(errch)
	for e := range errch {
		if e != nil {
			t.Fatal(e)
		}
	}
	rows, _ := s.View()
	if len(rows) != 32 {
		t.Fatal("concurrent write loss", len(rows))
	}
}
func TestAutomaticCheckpointAndLock(t *testing.T) {
	c := testkit.Config()
	dir := t.TempDir()
	s := openTest(t, dir, Options{CheckpointBytes: 1024})
	for i := 1; i <= 4; i++ {
		if e := s.Put(batch(c, testkit.Observation(c, "home-http", uint64(i), time.Now(), "ok"))); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := os.Stat(filepath.Join(dir, "checkpoint.json")); e != nil {
		t.Fatal("automatic checkpoint missing")
	}
	if other, e := Open(dir, Options{}); e == nil {
		other.Close()
		t.Fatal("second writer admitted")
	}
	s.Close()
	s = openTest(t, dir, Options{})
	rows, _ := s.View()
	if len(rows) != 4 {
		t.Fatal("checkpoint lost observations")
	}
}
func FuzzEnvelope(f *testing.F) {
	b, _ := wrap(map[string]string{"valid": "payload"})
	f.Add(b)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<16 {
			return
		}
		p, e := unwrap(b)
		if e == nil && !json.Valid(p) {
			t.Fatal("unwrapped invalid payload")
		}
	})
}

// After a reload the store keeps state only for the series it now counts,
// and a series counted again, as when an edit is undone, has its tail
// back from the history still held.
func TestSetCurrentFollowsAReload(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Now: func() time.Time { return now }})
	v := testkit.Observation(c, "home-http", 1, now.Add(-time.Second), "fail")
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal(e)
	}
	edited := testkit.Config()
	edited.Checks[0].HTTP.Status = []int{204}
	s.SetCurrent(config.Revision(edited), config.Contracts(edited), config.Stale(edited))
	if len(s.tails) != 0 || len(s.History("home-http", config.Contracts(edited), config.Revision(edited), 10)) != 0 {
		t.Fatal("a series no longer counted kept its tail or history", len(s.tails))
	}
	s.SetCurrent(config.Revision(c), config.Contracts(c), config.Stale(c))
	if n := len(s.tails[tailKey(v)]); n != 1 {
		t.Fatal("undoing the edit did not bring the tail back", n)
	}
	if rows, _, _ := s.StateView(); len(rows) != 1 {
		t.Fatal("history lost across reloads", len(rows))
	}
}
