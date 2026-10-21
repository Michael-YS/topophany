package engine

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestPlanOwnsInputsAndEveryPublishedSnapshot(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	rows := []model.Observation{testkit.Observation(c, "home-http", 1, now, "ok")}
	p := NewPlan(c)
	want := p.Build("test", "", rows, nil, nil, now)
	raw, _ := json.Marshal(want)
	c.Nodes[0].Name = "changed"
	c.Checks[0].Disabled = true
	want.Topology.Nodes[0].Name = "changed"
	want.Topology.Services[0].Endpoints[0] = "changed"
	want.Checks[0].Last.Facts["response_proof"] = "changed"
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got := p.Build("test", "", rows, nil, nil, now)
			encoded, _ := json.Marshal(got)
			if string(encoded) != string(raw) {
				t.Error("plan or published snapshot was mutated")
			}
		})
	}
	wg.Wait()
	if rows[0].Facts["response_proof"] != "http_response" {
		t.Fatal("snapshot mutated evidence")
	}
}

func TestPausedChecksDoNotDegradeActiveServiceEvidence(t *testing.T) {
	states := []model.CheckState{{ID: "up", Health: "up", Freshness: "fresh"}, {ID: "paused", Health: "unknown", Freshness: "paused"}, {ID: "maintenance", Health: "unknown", Freshness: "maintenance"}}
	if s := Aggregate("service", states, ""); s.Health != "up" || len(s.Checks) != 3 {
		t.Fatal(s)
	}
	if s := Aggregate("service", states[1:], ""); s.Health != "unknown" {
		t.Fatal(s)
	}
	states[1].Freshness = "stale"
	if s := Aggregate("service", states, ""); s.Health != "degraded" {
		t.Fatal(s)
	}
}

func BenchmarkEngineTopology(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			c := model.Config{Schema: 1, Project: "audit", Observers: []model.Observer{{ID: "worker", Context: "audit"}}}
			now := time.Now().UTC()
			rows := []model.Observation{}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("n%d", i)
				c.Nodes = append(c.Nodes, model.Node{ID: id})
				c.Endpoints = append(c.Endpoints, model.Endpoint{ID: id, Node: id, Address: "https://example.invalid", Network: "public"})
				c.Checks = append(c.Checks, model.Check{ID: id, Observer: "worker", Endpoint: id, Kind: "http"})
			}
			config.Defaults(&c)
			if e := config.Validate(c); e != nil {
				b.Fatal(e)
			}
			contracts := config.Contracts(c)
			for _, x := range c.Checks {
				for j := 0; j < 40; j++ {
					rows = append(rows, model.Observation{ID: fmt.Sprintf("worker.boot.%d", len(rows)+1), Boot: "boot", Sequence: uint64(len(rows) + 1), Project: c.Project, Contract: contracts[x.ID], Check: x.ID, Observer: "worker", SampledAt: now.Add(time.Duration(j-40) * time.Second), Outcome: "ok", Evidence: "measured"})
				}
			}
			b.Run("one-off", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					Build(c, "audit", "", rows, nil, now)
				}
			})
			p := NewPlan(c)
			prepared := p.Prepare(rows, nil, nil)
			b.Run("prepared", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					prepared.Build("audit", "", now)
				}
			})
			// Every new evidence version pays preparation again. Keep this
			// distinct from both cached reads and one-shot Plan.Build calls.
			b.Run("prepare-and-build", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					p.Prepare(rows, nil, nil).Build("audit", "", now)
				}
			})
			b.Run("generation", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					p.Build("audit", "", rows, nil, nil, now)
				}
			})
		})
	}
}
