package monitor

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func serverListTemplate() Template {
	t := Template{Schema: 1, Title: "Servers", Rows: []Row{{ID: "servers", Metric: "servers.list", Visual: "list"}}}
	t.Bar.Row = "servers"
	return t
}

func TestServerListMatchesServersTab(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	c, err := preview.Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	for sc, name := range preview.New(c, now, "en").ScenarioIDs() {
		t.Run(name, func(t *testing.T) {
			sim := preview.New(c, now, "en")
			sim.SetScenario(sc, now.Add(-30*time.Minute))
			snap, err := sim.Snapshot("", now)
			if err != nil {
				return // Collector outage has no snapshot to list.
			}
			v := view.View{Snapshot: snap, Now: now}
			rows := v.List(view.Servers, "")
			cfg := Config{Source: "preview", Template: serverListTemplate()}
			for i, row := range rows {
				cfg.Template.Rows = append(cfg.Template.Rows, Row{ID: fmt.Sprintf("card%d", i), Metric: "server.card", Visual: "card", Select: Selector{Node: row.ID}})
			}
			p := Build(cfg, Input{Snapshot: snap}, now)
			list := p.Rows[0].Value.([]ServerListEntry)
			if len(list) != len(rows) {
				t.Fatalf("list has %d entries, Servers tab has %d", len(list), len(rows))
			}
			for i, entry := range list {
				row := rows[i]
				if entry.ID != row.ID || entry.Name != row.Title || entry.Health != row.Health || entry.Note != row.Note {
					t.Fatalf("list differs from Servers tab: %+v / %+v", entry, row)
				}
				// The consumer can select every listed ID and gets the same metadata.
				card, ok := p.Rows[i+1].Value.(ServerCard)
				if !ok {
					t.Fatal("listed node has no readable card", entry.ID, p.Rows[i+1])
				}
				raw, _ := json.Marshal(card)
				var metadata ServerListEntry
				if err := json.Unmarshal(raw, &metadata); err != nil || metadata != entry {
					t.Fatal("list differs from card", entry, metadata, err)
				}
			}
		})
	}
}

func TestServerListFilteringBoundsAndCachedState(t *testing.T) {
	now := time.Now().UTC()
	snap := model.Snapshot{GeneratedAt: now, Topology: model.Config{Nodes: []model.Node{
		{ID: "external", Kind: "external"},
		{ID: "container", Kind: "container", Parent: "host"},
		{ID: "host", Name: "Zulu", Kind: "host", Group: "Home", Location: &model.Location{Label: "Seattle"}},
		{ID: "orphan", Kind: "container", Parent: "missing"},
		{ID: "edge", Kind: "edge", Provider: "cloudflare"},
		{ID: "platform", Kind: "platform"},
	}}}
	for i := 0; i < 255; i++ {
		snap.Topology.Nodes = append(snap.Topology.Nodes, model.Node{ID: fmt.Sprintf("n%03d", i), Name: "Alpha", Kind: "host"})
	}
	for _, n := range snap.Topology.Nodes {
		snap.Nodes = append(snap.Nodes, model.EntityState{ID: n.ID, Health: "up"})
	}
	for _, cached := range []bool{false, true} {
		v := view.View{Snapshot: snap, Now: now, Cached: cached}
		p := &projection{v: v, all: v}
		list, _, _ := p.servers()
		if len(list) != 256 || list[0].ID != "host" || list[1].ID != "orphan" || list[2].ID != "edge" || list[3].ID != "platform" || list[255].ID != "n251" {
			t.Fatal("incorrect filtering, order or bound", len(list), list[:4], list[len(list)-1])
		}
		if list[0].Where != "Seattle" || list[0].Group != "Home" || list[1].Name != "orphan" || list[2].Where != "anycast" || list[2].Provider != "Cloudflare" {
			t.Fatal("incorrect metadata", list[:4])
		}
		for _, entry := range list {
			if cached && (entry.Health != "unknown" || entry.Note != "cached") || !cached && entry.Health != "up" {
				t.Fatal(cached, entry)
			}
		}
	}
}

func TestServerListUsesOnlyListedNodeDeadlines(t *testing.T) {
	now := time.Now().UTC()
	snap := model.Snapshot{GeneratedAt: now, Topology: model.Config{Nodes: []model.Node{{ID: "host", Kind: "host"}, {ID: "excluded", Kind: "external"}}}}
	for _, spec := range []struct {
		id  string
		ttl int
	}{{"node", 6}, {"excluded", 2}, {"service", 2}, {"resource", 2}} {
		snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: spec.id, StaleSeconds: spec.ttl})
		snap.Checks = append(snap.Checks, model.CheckState{ID: spec.id, Health: "up", Freshness: "fresh", Last: &model.Observation{SampledAt: now.Add(-time.Second)}})
	}
	snap.Nodes = []model.EntityState{{ID: "host", Health: "up", Checks: []string{"node"}}, {ID: "excluded", Health: "down", Checks: []string{"excluded"}}}
	snap.Topology.Services = []model.Service{{ID: "svc", Node: "host"}}
	snap.Services = []model.EntityState{{ID: "svc", Health: "up", Checks: []string{"service"}}}
	snap.Topology.Observers = []model.Observer{{ID: "local", Node: "host", StaleSeconds: 2}}
	snap.Observers = []model.ObserverState{{ID: "local", Health: "up", LastSeen: now.Add(-time.Second)}}
	snap.Topology.Checks[3].Kind, snap.Topology.Checks[3].Observer = "host", "local"
	snap.Checks[3].Observer = "local"
	cfg := Config{Source: "client", Template: serverListTemplate()}
	p := Build(cfg, Input{Snapshot: snap}, now)
	r := p.Rows[0]
	if !r.ValidUntil.Equal(now.Add(5*time.Second)) || !r.SampledAt.Equal(now.Add(-time.Second)) || r.Health != "up" {
		t.Fatal("unrelated evidence bounded server choices", r)
	}
	for _, elapsed := range []time.Duration{5 * time.Second, 10 * time.Second} {
		if r := Build(cfg, Input{Snapshot: snap}, now.Add(elapsed)).Rows[0]; r.Value != nil || r.Freshness != "stale" || !r.ValidUntil.Equal(p.Rows[0].ValidUntil) {
			t.Fatal("producer revived an expired judgment", r)
		}
		p.PublisherExpiresAt = now.Add(time.Minute)
		if r := Current(p, now.Add(elapsed)).Rows[0]; r.Value != nil || r.Reason != "sample_expired" {
			t.Fatal("lease renewal extended evidence", r)
		}
	}
	// Even without supporting checks, the source envelope bounds the list.
	snap.Nodes[0].Checks = nil
	if r := Build(cfg, Input{Snapshot: snap}, now).Rows[0]; !r.ValidUntil.Equal(now.Add(model.SnapshotLifetime)) {
		t.Fatal(r)
	}
}

func TestServerListEmptyUnavailableAndValidation(t *testing.T) {
	now := time.Now().UTC()
	tpl := serverListTemplate()
	for _, source := range []string{"client", "collector", "preview"} {
		if err := tpl.Validate(source); err != nil {
			t.Fatal(source, err)
		}
	}
	if err := tpl.Validate("worker"); err == nil {
		t.Fatal("worker accepted server discovery")
	}
	for _, change := range []func(*Row){
		func(r *Row) { r.Visual = "card" },
		func(r *Row) { r.Select.Node = "host" },
		func(r *Row) { n := 5; r.Limit = &n },
	} {
		bad := serverListTemplate()
		change(&bad.Rows[0])
		if err := bad.Validate("client"); err == nil {
			t.Fatal("invalid list row accepted", bad)
		}
	}
	cfg := Config{Source: "preview", Template: tpl}
	p := Build(cfg, Input{Snapshot: model.Snapshot{GeneratedAt: now}}, now)
	raw, err := json.Marshal(p.Rows[0].Value)
	if err != nil || string(raw) != "[]" || p.Rows[0].Health != "up" || p.Rows[0].Text != "0 servers" {
		t.Fatal(p.Rows[0], string(raw), err)
	}
	if r := Build(cfg, Input{Unavailable: true}, now).Rows[0]; r.Value != nil || r.Reason != "source_unavailable" {
		t.Fatal(r)
	}
}

func TestServerListOwnershipAndImmediateTransitions(t *testing.T) {
	now := time.Now().UTC()
	last := Panel{Lifecycle: "running", GeneratedAt: now, Rows: []Value{{Row: Row{ID: "servers", Metric: "servers.list"}, Health: "down", Freshness: "fresh", Value: []ServerListEntry{{ID: "a", Health: "down"}, {ID: "b", Health: "up"}}}}}
	copy := last.Clone()
	copy.Rows[0].Value.([]ServerListEntry)[1].Name = "Edited"
	if last.Rows[0].Value.([]ServerListEntry)[1].Name != "" {
		t.Fatal("clone shares list storage")
	}
	for _, change := range []func([]ServerListEntry) []ServerListEntry{
		func(v []ServerListEntry) []ServerListEntry { v[1].Health = "unknown"; return v },
		func(v []ServerListEntry) []ServerListEntry { v[1].Note = "affected"; return v },
		func(v []ServerListEntry) []ServerListEntry { return v[:1] },
		func(v []ServerListEntry) []ServerListEntry { slices.Reverse(v); return v },
	} {
		next := last.Clone()
		next.GeneratedAt = now.Add(time.Second)
		next.Rows[0].Value = change(next.Rows[0].Value.([]ServerListEntry))
		if reflect.DeepEqual(last.Rows[0].Value, next.Rows[0].Value) || !ShouldPublish(last, ContentDigest(last), next, time.Hour) {
			t.Fatal("list transition waited for cadence", next)
		}
	}
}
