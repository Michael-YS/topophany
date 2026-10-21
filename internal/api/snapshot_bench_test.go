package api

import (
	"net/http"
	"testing"
	"time"
)

// Force a new capture each iteration: cache-hit-only benchmarks hide the cost
// paid by a single viewer polling after the one-second cache expires.
func BenchmarkSnapshotRepresentation(b *testing.B) {
	for _, mode := range []string{"identity", "gzip", "dynamic-gzip", "identity-then-gzip"} {
		b.Run(mode, func(b *testing.B) {
			s, _, _ := fixture(b)
			clock := s.Now()
			s.Now = func() time.Time { return clock }
			g := s.generation()
			revision := ""
			if mode == "dynamic-gzip" {
				revision = g.publicRevision
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				clock = clock.Add(2 * time.Second)
				if mode == "identity-then-gzip" {
					if _, status, code := s.cachedSnapshot(g, "", "viewer"); status != http.StatusOK {
						b.Fatal(code)
					}
				}
				body, status, code := s.snapshotRepresentation(g, "", "viewer", mode != "identity", revision)
				if status != http.StatusOK || len(body.data) == 0 {
					b.Fatal(status, code)
				}
			}
		})
	}
}
