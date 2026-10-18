package monitor

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestStructuredRowsKeepOriginalEvidenceDeadline(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	c := testkit.Config()
	var samples []model.Observation
	for i := range c.Checks {
		c.Checks[i].StaleSeconds = 60
		samples = append(samples, testkit.Observation(c, c.Checks[i].ID, uint64(i+1), now.Add(-59*time.Second), "ok"))
	}
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	snap := engine.Build(c, "fixture", "", samples, nil, now)
	original, _ := json.Marshal(snap)
	cfg := Config{Source: "client", Template: richTemplate()}
	cfg.Template.Rows[4].Select.Node = "dmit"
	first := Build(cfg, Input{Snapshot: snap}, now)
	for _, elapsed := range []time.Duration{time.Second, 2 * time.Second, 10 * time.Second} {
		p := Build(cfg, Input{Snapshot: snap}, now.Add(elapsed))
		for i, r := range p.Rows {
			if first.Rows[i].Value == nil || !r.ValidUntil.Equal(now.Add(time.Second)) || r.Value != nil || r.Health != "unknown" || r.Freshness != "stale" {
				t.Fatalf("%s at %s: first=%+v after=%+v", r.Metric, elapsed, first.Rows[i], r)
			}
		}
	}
	after, _ := json.Marshal(snap)
	if string(original) != string(after) {
		t.Fatal("projection mutated source")
	}
	// A new collector judgment can describe unknown evidence. It must not
	// revive the old healthy service when publishing that new snapshot.
	at := now.Add(2 * time.Second)
	fresh := engine.Build(c, "fixture", "", samples, nil, at)
	p := Build(cfg, Input{Snapshot: fresh}, at)
	for _, a := range p.Rows[3].Value.([]ApplicationValue) {
		for _, s := range a.Services {
			if s.Health == "up" {
				t.Fatal("expired service revived", s)
			}
		}
	}
	if p.Rows[4].Value.(ServerCard).Health == "up" {
		t.Fatal("expired node revived")
	}
}

func TestServerDeadlineIncludesObserverEvidence(t *testing.T) {
	for _, kind := range []string{"heartbeat", "check"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now().UTC()
			c := testkit.Config()
			var samples []model.Observation
			beat := now
			if kind == "heartbeat" {
				c.Observers[0].StaleSeconds = 10
				beat = now.Add(-9 * time.Second)
			} else {
				c.Checks[0].StaleSeconds = 60
				samples = []model.Observation{testkit.Observation(c, c.Checks[0].ID, 1, now.Add(-59*time.Second), "ok")}
			}
			snap := engine.Build(c, "fixture", "", samples, map[string]model.Heartbeat{"home": {Project: c.Project, SampledAt: beat}}, now)
			cfg := Config{Source: "client", Template: Template{Rows: []Row{{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "home"}}}}}
			p := Build(cfg, Input{Snapshot: snap}, now)
			if !p.Rows[0].ValidUntil.Equal(now.Add(time.Second)) {
				t.Fatal(p.Rows[0])
			}
			p.PublisherExpiresAt = now.Add(30 * time.Second)
			if Current(p, now.Add(time.Second)).Rows[0].Value != nil {
				t.Fatal("reader retained expired nested evidence")
			}
		})
	}
}

func TestSummaryLivenessBypassesCadence(t *testing.T) {
	now := time.Now().UTC()
	s := SummaryVerdict{}
	s.Observers.Live, s.Observers.All = 1, 1
	last := Panel{Lifecycle: "running", GeneratedAt: now, Rows: []Value{{Row: Row{ID: "status"}, Health: "up", Freshness: "fresh", Value: s}}}
	next := clone(last)
	next.GeneratedAt = now.Add(time.Second)
	s.Observers.Live = 0
	next.Rows[0].Value = s
	if !ShouldPublish(last, ContentDigest(last), next, time.Hour) {
		t.Fatal("liveness transition waited for cadence")
	}
}

func TestCardNodeStateMatchesTopology(t *testing.T) {
	c := testkit.Config()
	for _, cached := range []bool{false, true} {
		for _, health := range []string{"up", "down", "unknown", "degraded"} {
			snap := model.Snapshot{Topology: c, Nodes: []model.EntityState{{ID: "home", Health: health, Affected: true}}}
			v := view.View{Snapshot: snap, Cached: cached}
			fleet := v.Fleet()
			for _, r := range v.List(view.Topology, "") {
				if r.Kind == "node" {
					h, n := fleet.NodeState(r.ID)
					if h != r.Health || n != r.Note {
						t.Fatal(r, h, n)
					}
				}
			}
		}
	}
}

func BenchmarkServerPanels(b *testing.B) {
	now := time.Now().UTC()
	snap := model.Snapshot{GeneratedAt: now, Topology: model.Config{Observers: []model.Observer{{ID: "local", StaleSeconds: 90}}}, Observers: []model.ObserverState{{ID: "local", Health: "up", LastSeen: now}}}
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("n%d", i)
		snap.Topology.Nodes = append(snap.Topology.Nodes, model.Node{ID: id, Name: id, Kind: "host"})
		snap.Topology.Endpoints = append(snap.Topology.Endpoints, model.Endpoint{ID: id, Node: id, Address: "https://example.invalid", Network: "public"})
		snap.Topology.Services = append(snap.Topology.Services, model.Service{ID: id, Node: id, Endpoints: []string{id}})
		snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: id, Kind: "http", Observer: "local", Endpoint: id, Service: id, StaleSeconds: 90})
		snap.Nodes = append(snap.Nodes, model.EntityState{ID: id, Health: "up", Checks: []string{id}})
		snap.Endpoints = append(snap.Endpoints, model.EntityState{ID: id, Health: "up", Checks: []string{id}})
		snap.Services = append(snap.Services, model.EntityState{ID: id, Health: "up", Checks: []string{id}})
		snap.Checks = append(snap.Checks, model.CheckState{ID: id, Observer: "local", Health: "up", Freshness: "fresh", Last: &model.Observation{SampledAt: now}})
	}
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			cfg := Config{Source: "client"}
			for i := 0; i < count; i++ {
				cfg.Template.Rows = append(cfg.Template.Rows, Row{ID: fmt.Sprintf("r%d", i), Metric: "server.card", Visual: "card", Select: Selector{Node: fmt.Sprintf("n%d", i)}})
			}
			b.ReportAllocs()
			for b.Loop() {
				Build(cfg, Input{Snapshot: snap}, now)
			}
		})
	}
}
