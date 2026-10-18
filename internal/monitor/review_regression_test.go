package monitor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestServerTransitiveDependencyDeadline(t *testing.T) {
	for _, kind := range []string{"service", "path-candidate", "path-dependency"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now().UTC()
			c := model.Config{Schema: 1, Project: "lab",
				Nodes:     []model.Node{{ID: "a"}, {ID: "b"}, {ID: "observer"}},
				Observers: []model.Observer{{ID: "o", Node: "observer", Context: "test", StaleSeconds: 90}},
				Endpoints: []model.Endpoint{{ID: "a", Node: "a", Address: "https://example.invalid/a", Network: "public"}, {ID: "b", Node: "b", Address: "https://example.invalid/b", Network: "public"}},
				Services:  []model.Service{{ID: "a", Node: "a", Endpoints: []string{"a"}, DependsOn: []string{"middle"}}, {ID: "middle", Node: "b", DependsOn: []string{"b"}}, {ID: "b", Node: "b", Endpoints: []string{"b"}}},
				Checks:    []model.Check{{ID: "a", Observer: "o", Endpoint: "a", Service: "a", Kind: "http", StaleSeconds: 60, FailureThreshold: 1}, {ID: "b", Observer: "o", Endpoint: "b", Service: "b", Kind: "http", StaleSeconds: 60, FailureThreshold: 1}},
			}
			if kind != "service" {
				c.Services[0].DependsOn = nil
				c.Checks[0].Path = "outer"
				c.Checks[1].Path = "leaf"
				c.Paths = []model.Path{{ID: "outer", Observer: "o", Nodes: []string{"observer", "a"}, DependsOn: []string{"middle"}}, {ID: "middle", Observer: "o", Candidates: []string{"leaf"}}, {ID: "leaf", Observer: "o", Nodes: []string{"observer", "b"}}}
				if kind == "path-dependency" {
					c.Paths[1].Candidates = nil
					c.Paths[1].Nodes = []string{"observer", "a"}
					c.Paths[1].DependsOn = []string{"leaf"}
				}
			}
			config.Defaults(&c)
			if err := config.Validate(c); err != nil {
				t.Fatal(err)
			}
			observations := []model.Observation{testkit.Observation(c, "a", 1, now, "ok"), testkit.Observation(c, "b", 2, now.Add(-59*time.Second), "fail")}
			snap := engine.Build(c, "fixture", "", observations, nil, now)
			cfg := Config{Template: Template{Rows: []Row{{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "a"}}}}}
			p := Build(cfg, Input{Snapshot: snap}, now)
			r := p.Rows[0]
			if r.Value == nil || !r.ValidUntil.Equal(now.Add(time.Second)) {
				t.Fatalf("missing transitive dependency deadline: %+v", r)
			}
			if kind == "service" && !r.Value.(ServerCard).Services[0].Affected {
				t.Fatal("fixture did not establish dependency impact")
			}
			p.PublisherExpiresAt = now.Add(30 * time.Second)
			if Current(p, now.Add(time.Second)).Rows[0].Value != nil {
				t.Fatal("reader kept expired dependency judgment")
			}
			if Build(cfg, Input{Snapshot: snap}, now.Add(time.Second)).Rows[0].Value != nil {
				t.Fatal("producer kept expired dependency judgment")
			}
		})
	}
}

func TestScopedPanelPreservesKnownObserverEvidence(t *testing.T) {
	now := time.Now().UTC()
	c := testkit.Config()
	var observations []model.Observation
	for i, x := range c.Checks {
		observations = append(observations, testkit.Observation(c, x.ID, uint64(i+1), now, "ok"))
	}
	scoped := engine.Build(c, "fixture", "home", observations, nil, now)
	all := engine.Build(c, "fixture", "", observations, nil, now)
	cfg := Config{Source: "client", Observer: "home", Template: Template{Rows: []Row{{ID: "summary", Metric: "summary.verdict", Visual: "status"}, {ID: "observers", Metric: "observers.verdicts", Visual: "list"}, {ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "home"}}}}}
	for _, complete := range []bool{false, true} {
		in := Input{Snapshot: scoped}
		if complete {
			in.AllSnapshot = &all
		}
		p := Build(cfg, in, now)
		summary := p.Rows[0].Value.(SummaryVerdict)
		observers := p.Rows[1].Value.([]ObserverVerdict)
		for _, o := range observers {
			if !o.Live {
				t.Fatal("scoped feed lost global heartbeat", o)
			}
			if o.ID == "home" && (o.State != summary.State || o.Counts != summary.Counts.Counts) {
				t.Fatal("selected observer disagrees with summary", o, summary)
			}
			if o.ID == "cloud" {
				if !complete && (o.State != "cached" || o.Health != "unknown" || o.Counts.Unknown != 1) {
					t.Fatal("invented an unprovided verdict", o)
				}
				if complete && (o.State != "clear" || o.Counts.Up != 1) {
					t.Fatal("complete snapshot ignored", o)
				}
			}
		}
		own := p.Rows[2].Value.(ServerCard).Observes[0]
		if own.State != summary.State || !own.Live {
			t.Fatal("card lost selected observer", own)
		}
	}
}

func TestServerResourceExpiryPreservesCard(t *testing.T) {
	now := time.Now().UTC()
	for _, freshness := range []string{"fresh", "stale", "paused", "maintenance"} {
		t.Run(freshness, func(t *testing.T) {
			in := hostInput(now.Add(-9 * time.Second))
			in.Snapshot.GeneratedAt = now
			in.Snapshot.Topology.Nodes = []model.Node{{ID: "host", Name: "My host"}}
			in.Snapshot.Topology.Observers[0].Node = "host"
			in.Snapshot.Topology.Observers[0].StaleSeconds = 90
			in.Snapshot.Observers = []model.ObserverState{{ID: "local", Health: "up", LastSeen: now}}
			in.Snapshot.Checks[0].Freshness = freshness
			if freshness != "fresh" {
				in.Snapshot.Checks[0].Last.SampledAt = now.Add(-time.Minute)
				in.Snapshot.Checks[0].Health = "unknown"
			}
			in.Snapshot.Topology.Checks[0].Host = &model.HostOptions{ProcessName: "topo", MaxCPUPercent: 90, MaxMemoryPercent: 90, MaxDiskPercent: 90}
			in.Snapshot.Checks[0].Last.Facts = map[string]string{"cpu_used_percent": "42", "cpu_logical_count": "8", "memory_used_percent": "20", "memory_used_bytes": "200", "memory_total_bytes": "1000", "disk_used_percent": "50", "disk_used_bytes": "500", "disk_total_bytes": "1000", "process_matching_count": "1"}
			cfg := Config{Template: Template{Rows: []Row{{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "host"}}}}}
			p := Build(cfg, in, now)
			p.PublisherExpiresAt = now.Add(30 * time.Second)
			original, _ := json.Marshal(p)
			if p.Rows[0].Value == nil || !p.Rows[0].ValidUntil.Equal(now.Add(15*time.Second)) {
				t.Fatal("resource expired entire card", p.Rows[0])
			}
			if freshness == "fresh" && p.Rows[0].Value.(ServerCard).Resources[0].CPU.Percent == nil {
				t.Fatal("fresh number missing")
			}
			at := now.Add(time.Second)
			for _, decoded := range []bool{false, true} {
				input := p
				if decoded {
					input = Panel{}
					if err := json.Unmarshal(original, &input); err != nil {
						t.Fatal(err)
					}
				}
				before, _ := json.Marshal(input)
				got := Current(input, at).Rows[0]
				assertExpiredResourceCard(t, got, in.Snapshot.Checks[0].Last.SampledAt)
				retained, _ := json.Marshal(input)
				if string(retained) != string(before) {
					t.Fatal("reader mutated its input")
				}
			}
			assertExpiredResourceCard(t, Build(cfg, in, at).Rows[0], in.Snapshot.Checks[0].Last.SampledAt)
			if Current(p, now.Add(15*time.Second)).Rows[0].Value != nil {
				t.Fatal("card outlived source envelope")
			}
			v := view.View{Snapshot: in.Snapshot, Now: at.Add(time.Nanosecond)}
			if v.Fleet().Card("host").Resources[0].Facts != nil {
				t.Fatal("fixture resource still readable")
			}
		})
	}
}
func assertExpiredResourceCard(t *testing.T, r Value, sampled time.Time) {
	t.Helper()
	card, ok := r.Value.(ServerCard)
	if !ok || card.Name != "My host" || len(card.Resources) != 1 {
		t.Fatal("card information lost", r)
	}
	resource := card.Resources[0]
	if resource.SampledAt == nil || !resource.SampledAt.Equal(sampled) || resource.CPU.Percent != nil || resource.CPU.Limit != nil || resource.CPU.Logical != nil || resource.Memory.Percent != nil || resource.Memory.Limit != nil || resource.Memory.Used != nil || resource.Memory.Total != nil || resource.Disk.Percent != nil || resource.Disk.Limit != nil || resource.Disk.Used != nil || resource.Disk.Total != nil || resource.Process == nil || resource.Process.Name != "topo" || resource.Process.Matching != nil {
		t.Fatal("stale resource exposed or identity lost", resource)
	}
}
