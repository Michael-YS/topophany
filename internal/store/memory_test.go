package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestStateViewUsesTailsWithoutChangingEvaluation(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c)})
	rows := flapping(c, "home-http", 1001, now.Add(-time.Hour), time.Second)
	for _, v := range rows {
		s.addRow(v)
	}
	s.prune()
	got, cs, beats := s.StateView()
	if len(got) > tailLength {
		t.Fatal("state view copied raw history", len(got))
	}
	want := engine.BuildCarried(c, "a", "", rows, cs, beats, now)
	actual := engine.BuildCarried(c, "a", "", got, cs, beats, now)
	wantJSON, _ := json.Marshal(want)
	actualJSON, _ := json.Marshal(actual)
	if !bytes.Equal(wantJSON, actualJSON) {
		t.Fatalf("bounded projection changed evaluated state: want %s got %s", wantJSON, actualJSON)
	}
}

func TestPruneReleasesExpiredBackingReferences(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Retention: time.Minute, Now: func() time.Time { return now }})
	for i := 0; i < 100; i++ {
		v := testkit.Observation(c, "home-http", uint64(i+1), now, "ok")
		s.addRow(v)
	}
	full := s.rows
	now = now.Add(2 * time.Hour)
	s.prune()
	if len(s.rows) != 0 {
		t.Fatal("expired history retained")
	}
	for _, v := range full {
		if v.ID != "" || v.Facts != nil || v.Timings != nil {
			t.Fatal("unused history backing array retains expired evidence")
		}
	}
}

func TestStreamedCheckpointPreservesStateAndChecksum(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	s := openTest(t, dir, Options{Now: func() time.Time { return now }, MaxHistory: 10})
	for i := 0; i < 60; i++ {
		v := testkit.Observation(c, "home-http", uint64(i+1), now.Add(time.Duration(i)*time.Second), "ok")
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := unwrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	var disk diskState
	if err = json.Unmarshal(payload, &disk); err != nil {
		t.Fatal(err)
	}
	if len(disk.Rows) != tailLength || len(disk.Seen) != 60 {
		t.Fatal("checkpoint lost tails or receipts")
	}
	var envelope map[string]json.RawMessage
	json.Unmarshal(raw, &envelope)
	envelope["sha256"] = json.RawMessage(`"tampered"`)
	corrupt, _ := json.Marshal(envelope)
	if _, err = unwrap(corrupt); err == nil {
		t.Fatal("checksum not enforced")
	}
}

// Advance five days, across 120 receipt expiry windows and rollup compaction.
// Retention growth is expected during warmup; after saturation, collected heap
// and retained indexes must plateau. This is a bounded synthetic soak, not a
// claim that all production workloads are leak-free.
func TestCollectorMemoryPlateausAcrossRetentionWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("synthetic soak")
	}
	c := testkit.Config()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := openTest(t, t.TempDir(), Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Retention: time.Minute, RollupBytes: 64 << 10, Now: func() time.Time { return now }})
	ids := []string{"home-http", "home-ssh", "primary-http", "backup-http"}
	var baseline uint64
	for i := 1; i <= 1440; i++ {
		now = now.Add(5 * time.Minute)
		vs := make([]model.Observation, 0, len(ids))
		for j, id := range ids {
			v := testkit.Observation(c, id, uint64(i*len(ids)+j), now, "ok")
			// Change diagnostics continuously, so the sharing dictionary has
			// to rotate rather than growing with every retained sample.
			v.Detail = strings.Repeat("diagnostic ", 10) + strconv.Itoa(i*len(ids)+j)
			v.Facts["sample_counter"] = strconv.Itoa(i*len(ids) + j)
			vs = append(vs, v)
		}
		if err := s.Put(batch(c, vs...)); err != nil {
			t.Fatal(err)
		}
		s.StateView()
		if i%288 != 0 {
			continue
		}
		if err := s.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if len(s.seen) > 56 || len(s.rows) > 4 || len(s.rowSizes) > len(ids)*tailLength || len(s.carries) > len(ids) || len(s.receiptQueue) > 2048 {
			t.Fatalf("retained indexes grew beyond window: receipts=%d rows=%d rowSizes=%d carries=%d queue=%d", len(s.seen), len(s.rows), len(s.rowSizes), len(s.carries), len(s.receiptQueue))
		}
		if len(s.strings.current) > stringGenerationSize || len(s.strings.previous) > stringGenerationSize {
			t.Fatal("changing diagnostics grew the sharing dictionary without bound")
		}
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if baseline == 0 {
			baseline = m.HeapAlloc
		}
		t.Logf("day %d: collected heap=%d receipts=%d raw=%d tails=%d", i/288, m.HeapAlloc, len(s.seen), len(s.rows), len(s.tails))
		if m.HeapAlloc > baseline+4<<20 {
			t.Fatalf("collected heap did not plateau: initial=%d now=%d", baseline, m.HeapAlloc)
		}
	}
	runtime.KeepAlive(s)
}
