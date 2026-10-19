package store

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Set TOPO_BENCH_CHECKPOINT to profile a private copy of an operator's checkpoint.
// The source is only read; all recovery/checkpoint writes go into b.TempDir().
func memoryFixture(b testing.TB) *Store {
	b.Helper()
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	dir := b.TempDir()
	if path := os.Getenv("TOPO_BENCH_CHECKPOINT"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "checkpoint.json"), raw, 0600); err != nil {
			b.Fatal(err)
		}
	}
	s, err := Open(dir, Options{Now: func() time.Time { return now }, RollupBytes: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	if len(s.rows) != 0 {
		return s
	}
	c := testkit.Config()
	for i := 0; i < 20000; i++ {
		v := testkit.Observation(c, "home-http", uint64(i+1), now.Add(time.Duration(i-20000)*time.Second), "ok")
		// Eighty independent series, with 250 raw observations each.
		v.Check = v.Check + string(rune('A'+i%80))
		v.Contract = config.Contracts(c)["home-http"]
		v.ReceivedAt = v.SampledAt
		s.addRow(v)
	}
	s.prune()
	return s
}

func TestCollectorCheckpointMemory(t *testing.T) {
	if os.Getenv("TOPO_BENCH_CHECKPOINT") == "" {
		t.Skip("operator checkpoint profile is opt-in")
	}
	s := memoryFixture(t)
	for _, step := range []string{"recovered", "checkpoint"} {
		if step == "checkpoint" {
			if err := s.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		t.Logf("%s: collected_heap_bytes=%d runtime_bytes=%d receipts=%d history=%d tails=%d", step, m.HeapAlloc, m.Sys-m.HeapReleased, len(s.seen), len(s.rows), len(s.tails))
		if step == "checkpoint" && os.Getenv("TOPO_BENCH_HEAP") != "" {
			f, err := os.Create(os.Getenv("TOPO_BENCH_HEAP"))
			if err != nil {
				t.Fatal(err)
			}
			if err = pprof.WriteHeapProfile(f); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime.KeepAlive(s)
}

var memoryRows []model.Observation

func BenchmarkCollectorStateView(b *testing.B) {
	s := memoryFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		memoryRows, _, _ = s.StateView()
	}
}

func BenchmarkCollectorCheckpoint(b *testing.B) {
	s := memoryFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := s.checkpoint(); err != nil {
			b.Fatal(err)
		}
	}
}
