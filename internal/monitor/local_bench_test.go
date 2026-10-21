package monitor

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

var benchmarkLocalSnapshot model.Snapshot

func BenchmarkLocalPanelSnapshot(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			c := model.Config{Schema: 1, Project: "review", Observers: []model.Observer{{ID: "worker", Context: "review"}}}
			now := time.Now().UTC()
			for i := 0; i < n; i++ {
				id := fmt.Sprint("n", i)
				c.Nodes = append(c.Nodes, model.Node{ID: id})
				c.Endpoints = append(c.Endpoints, model.Endpoint{ID: id, Node: id, Address: "https://example.invalid", Network: "public"})
				c.Checks = append(c.Checks, model.Check{ID: id, Observer: "worker", Endpoint: id, Kind: "http"})
			}
			config.Defaults(&c)
			if err := config.Validate(c); err != nil {
				b.Fatal(err)
			}
			l := NewLocal(c, "worker")
			contracts := config.Contracts(c)
			for i, x := range c.Checks {
				l.Record(model.Observation{ID: fmt.Sprint("worker.boot.", i+1), Boot: "boot", Sequence: uint64(i + 1), Project: c.Project, Contract: contracts[x.ID], Check: x.ID, Observer: "worker", SampledAt: now, Outcome: "ok", Evidence: "measured"})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				benchmarkLocalSnapshot = l.Snapshot(now)
			}
		})
	}
}
