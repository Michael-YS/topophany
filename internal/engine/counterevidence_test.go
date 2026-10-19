package engine

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func concentratedTopology(t testing.TB, count int) (model.Config, []model.Observation, time.Time) {
	t.Helper()
	c := model.Config{Schema: 1, Project: "audit", Nodes: []model.Node{{ID: "node"}}, Endpoints: []model.Endpoint{{ID: "ep", Node: "node", Address: "https://example.invalid", Network: "public"}}, Observers: []model.Observer{{ID: "a", Context: "audit"}, {ID: "b", Context: "audit"}}}
	for i := 0; i < count; i++ {
		observer := "a"
		if i%2 == 1 {
			observer = "b"
		}
		c.Checks = append(c.Checks, model.Check{ID: fmt.Sprintf("c%d", i), Kind: "http", Observer: observer, Endpoint: "ep", FailureThreshold: 1})
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rows := make([]model.Observation, 0, count)
	contracts := config.Contracts(c)
	for i, x := range c.Checks {
		outcome := "fail"
		if i%2 == 1 {
			outcome = "ok"
		}
		rows = append(rows, model.Observation{ID: fmt.Sprintf("%s.boot.%d", x.Observer, i+1), Boot: "boot", Sequence: uint64(i + 1), Project: c.Project, Check: x.ID, Observer: x.Observer, Contract: contracts[x.ID], SampledAt: now, Outcome: outcome, Evidence: "measured", Connectivity: outcome == "ok", Facts: map[string]string{"response_proof": "verified_https"}})
	}
	return c, rows, now
}

func TestCounterEvidenceNearLimitKeepsCoreFailures(t *testing.T) {
	c, rows, now := concentratedTopology(t, 4000)
	p := NewPlan(c)
	s := p.Build("audit", "", rows, nil, nil, now)
	if len(s.Checks) != 4000 || len(s.Incidents) != 1 || len(s.Incidents[0].Checks) != 2000 || len(s.Incidents[0].CounterEvidence) != counterEvidenceLimit {
		t.Fatalf("lost core state or unbounded diagnostics: checks=%d incidents=%d", len(s.Checks), len(s.Incidents))
	}
	for _, id := range s.Incidents[0].CounterEvidence {
		if p.checks[id].Observer != "b" {
			t.Fatal("same-observer counterevidence", id)
		}
	}
}

func TestResponseProofIndexBoundsAndExclusion(t *testing.T) {
	c := testkit.Config()
	p := NewPlan(c)
	// Thousands of proofs from the first observer must not crowd out another
	// observer, and repeating a Via reference must not duplicate a proof.
	p.nodeChecks = map[string][]string{"node": {}}
	p.config.Checks = nil
	p.endpoints = map[string]model.Endpoint{"ep": {ID: "ep", Node: "node"}}
	states := map[string]model.CheckState{}
	for i := 0; i < 4000; i++ {
		id := fmt.Sprintf("proof%d", i)
		observer := "a"
		if i >= 3000 {
			observer = "b"
		}
		p.checks[id] = model.Check{ID: id, Observer: observer, Endpoint: "ep"}
		p.config.Checks = append(p.config.Checks, p.checks[id])
		p.nodeChecks["node"] = append(p.nodeChecks["node"], id, id)
		states[id] = model.CheckState{Freshness: "fresh", Last: &model.Observation{Evidence: "measured", Connectivity: true, Facts: map[string]string{"response_proof": "verified_https"}}}
	}
	index := p.responseProofs(states)
	if len(index["node"]) != 64 {
		t.Fatal(len(index["node"]))
	}
	for _, observer := range []string{"a", "b", "third"} {
		ids := index.excluding("node", observer)
		if len(ids) != 32 {
			t.Fatal(observer, len(ids))
		}
		for i, id := range ids {
			if p.checks[id].Observer == observer || slices.Contains(ids[:i], id) {
				t.Fatal("invalid counterevidence", id)
			}
		}
	}
	// The saturated aggregation helper returns immediately, with no map rebuild.
	dst := make([]string, 32)
	if allocations := testing.AllocsPerRun(100, func() { boundedChecks(dst, []string{"extra"}) }); allocations != 0 {
		t.Fatal("full diagnostic still allocates", allocations)
	}
	// Exclusion inspects only the bounded index, regardless of topology size.
	if allocations := testing.AllocsPerRun(100, func() { index.excluding("node", "a") }); allocations > 7 {
		t.Fatal("unbounded lookup allocations", allocations)
	}
}

func BenchmarkCrossObserverCounterEvidence(b *testing.B) {
	for _, count := range []int{1000, 4000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			c, rows, now := concentratedTopology(b, count)
			p := NewPlan(c)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.Build("audit", "", rows, nil, nil, now)
			}
		})
	}
}
