package app

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestMemoryFlagsValidateBeforeOpeningData(t *testing.T) {
	for _, role := range []string{"serve", "worker"} {
		for _, args := range [][]string{{"--memory-limit", "1MiB"}, {"--memory-limit", "oops"}, {"--memory-max", "1MiB"}, {"--memory-limit", "128MiB", "--memory-max", "128MiB"}} {
			a := &App{Out: io.Discard, Err: io.Discard, In: strings.NewReader("")}
			e := a.Run(context.Background(), append([]string{role}, args...))
			if e == nil || !strings.Contains(e.Error(), "memory") {
				t.Fatalf("%s %v: %v", role, args, e)
			}
		}
	}
}
func TestMemoryLogReportsCollectedHeapAndPolicy(t *testing.T) {
	var out bytes.Buffer
	a := &App{Err: &out}
	a.logMemory()
	for _, s := range []string{"heap_live_bytes=", "heap_alloc_bytes=", "runtime_bytes=", "gc_cycles=", "goroutines=", "soft_limit_bytes="} {
		if !strings.Contains(out.String(), s) {
			t.Fatal(out.String())
		}
	}
}
