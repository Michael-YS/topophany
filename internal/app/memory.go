package app

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"

	"github.com/WhitenWhiten/topophany/internal/memlimit"
)

type memoryFlags struct {
	soft, hard *string
	flags      *flag.FlagSet
}

func addMemoryFlags(f *flag.FlagSet) memoryFlags {
	// Preserve an explicitly configured GOMEMLIMIT when no CLI override is used.
	target := "128MiB"
	if f.Name() == "worker" {
		target = "64MiB"
	}
	if os.Getenv("GOMEMLIMIT") != "" {
		target = "0"
	}
	return memoryFlags{
		f.String("memory-limit", target, "Go runtime SOFT memory target (bytes or e.g. 128MiB); 0 preserves GOMEMLIMIT"),
		f.String("memory-max", "0", "kernel HARD cap incl. child processes, no swap (Linux cgroup v2 only); unsupported/unavailable fails startup"),
		f,
	}
}

func (m memoryFlags) apply() (func(), error) {
	soft, err := memlimit.Parse(*m.soft)
	if err != nil {
		return nil, fmt.Errorf("memory-limit: %w", err)
	}
	hard, err := memlimit.Parse(*m.hard)
	if err != nil {
		return nil, fmt.Errorf("memory-max: %w", err)
	}
	explicit := false
	m.flags.Visit(func(f *flag.Flag) {
		if f.Name == "memory-limit" {
			explicit = true
		}
	})
	if hard > 0 && !explicit {
		soft = 0
	}
	return memlimit.Apply(soft, hard)
}

func (a *App) logMemory() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	samples := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(samples)
	fmt.Fprintf(a.Err, "memory_since_start heap_live_bytes=%d heap_alloc_bytes=%d runtime_bytes=%d next_gc_bytes=%d gc_cycles=%d goroutines=%d soft_limit_bytes=%d\n", samples[0].Value.Uint64(), m.HeapAlloc, m.Sys-m.HeapReleased, m.NextGC, m.NumGC, runtime.NumGoroutine(), debug.SetMemoryLimit(-1))
}
