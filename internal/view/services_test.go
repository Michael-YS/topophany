package view_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// previewView is the built-in network in a scenario, its outage begun
// four minutes ago.
func previewView(t *testing.T, scenario string) view.View {
	t.Helper()
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	c, err := preview.Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	sim := preview.New(c, now, "en")
	if scenario != "" {
		sim.SetScenario(slices.Index(sim.ScenarioIDs(), scenario), now.Add(-4*time.Minute))
	}
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	return view.View{Snapshot: snap, Now: now}
}

func TestServiceCardSaysWhereItRunsAndWhoSeesIt(t *testing.T) {
	v := previewView(t, "")
	c, ok := v.Fleet().ServiceCard("blog")
	if !ok {
		t.Fatal("no card for a declared service")
	}
	if c.Name != "Main Site (Caddy + Blog)" || c.Application != (view.Ref{ID: "main-site", Name: "Main site"}) || c.Health != "up" || c.Note != "" || !c.Monitored {
		t.Fatalf("%+v", c)
	}
	if c.Host.ID != "app" || c.Host.Name != "Oracle New York" || c.Host.Provider != "Oracle" || c.Host.Where != "New York" || c.Host.Health != "up" {
		t.Fatalf("host %+v", c.Host)
	}
	labels := []string{}
	for _, s := range c.SeenFrom {
		labels = append(labels, s.Label)
		if len(s.Checks) == 0 || s.Health != s.Checks[0].Health {
			t.Fatalf("%+v", s)
		}
	}
	slices.Sort(labels)
	if !slices.Equal(labels, []string{"Frankfurt", "New York", "San Jose"}) {
		t.Fatal(labels)
	}
	for _, s := range c.SeenFrom {
		x := s.Checks[0]
		if x.Latency == nil || x.Up24h == nil || *x.Up24h <= 0 || *x.Up24h > 100 || x.Interval <= 0 {
			t.Fatalf("%+v", x)
		}
	}
	if len(c.Endpoints) != 2 || c.Endpoints[0].Network != "public" || len(c.Endpoints[0].Via) != 1 || c.Endpoints[0].Via[0].ID != "cf" {
		t.Fatalf("%+v", c.Endpoints)
	}
	if c.Answered == nil || len(c.Answered.Pops) == 0 || !slices.ContainsFunc(c.Answered.Cache, func(s string) bool { return strings.HasPrefix(s, "DYNAMIC ") }) {
		t.Fatalf("%+v", c.Answered)
	}
	if len(c.DependsOn) != 1 || c.DependsOn[0].ID != "postgres" || c.DependsOn[0].Host != "PostgreSQL Container" {
		t.Fatalf("%+v", c.DependsOn)
	}
	if c.Runbook != "" || c.Memo != "" || len(c.UsedBy) != 0 {
		t.Fatalf("%+v", c)
	}
	pg, _ := v.Fleet().ServiceCard("postgres")
	used := []string{}
	for _, r := range pg.UsedBy {
		used = append(used, r.ID)
	}
	slices.Sort(used)
	if !slices.Equal(used, []string{"api", "backup-job", "blog"}) || pg.Host.Where != "New York" {
		t.Fatalf("%v %q", used, pg.Host.Where)
	}
	if _, ok := v.Fleet().ServiceCard("nope"); ok {
		t.Fatal("card for an undeclared service")
	}
}

// Two observers in one city, each on two paths, read as two rows, each
// with its checks worst first; neither is merged into the other.
func TestServiceCardKeepsEachObserversChecks(t *testing.T) {
	c, _ := previewView(t, "transit-blocked").Fleet().ServiceCard("egress-a")
	if c.Note != "affected" || len(c.SeenFrom) != 2 {
		t.Fatalf("%q %+v", c.Note, c.SeenFrom)
	}
	for _, s := range c.SeenFrom {
		if s.Label != "Windows" && s.Label != "MacBook" {
			t.Fatalf("label %q", s.Label)
		}
		if len(s.Checks) != 2 || s.Checks[0].Health != "down" || s.Checks[1].Health != "up" || s.Health != "down" {
			t.Fatalf("%+v", s.Checks)
		}
	}
	vless, _ := previewView(t, "transit-blocked").Fleet().ServiceCard("vless-transit")
	if vless.Health != "degraded" || vless.SeenFrom[0].Health != "down" || vless.SeenFrom[len(vless.SeenFrom)-1].Health != "up" {
		t.Fatalf("%+v", vless.SeenFrom)
	}
}

func TestServiceCardWithoutChecksSaysSo(t *testing.T) {
	c, _ := previewView(t, "").Fleet().ServiceCard("backup-job")
	if c.Monitored || len(c.SeenFrom) != 0 || c.Health != "unknown" || c.Memo == "" || len(c.DependsOn) != 2 {
		t.Fatalf("%+v", c)
	}
}

// A cached view knows no state and no current reading; the day's rollups
// still describe what was seen.
func TestCachedServiceCardWithholdsTheCurrent(t *testing.T) {
	v := previewView(t, "")
	v.Cached = true
	c, _ := v.Fleet().ServiceCard("shortlink")
	if c.Health != "unknown" || c.Note != "cached" || c.Reason != "" || c.Host.Health != "unknown" {
		t.Fatalf("%+v", c)
	}
	for _, s := range c.SeenFrom {
		for _, x := range s.Checks {
			if x.Health != "unknown" || x.Freshness != "stale" || x.Colo != "" || x.Facts != nil || x.Latency == nil {
				t.Fatalf("%+v", x)
			}
		}
	}
	live, _ := previewView(t, "").Fleet().ServiceCard("shortlink")
	colos := []string{}
	for _, s := range live.SeenFrom {
		colos = append(colos, s.Checks[0].Colo)
	}
	slices.Sort(colos)
	if !slices.Equal(colos, []string{"FRA", "SJC"}) {
		t.Fatal(colos)
	}
}

// In a view scoped to one observer, a service only other observers measure
// is not seen from here rather than unmonitored; in the whole view nothing
// is elsewhere.
func TestScopedServiceCardIsSeenElsewhere(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	c, err := preview.Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := preview.New(c, now, "en").Snapshot("obs-mac", now)
	if err != nil {
		t.Fatal(err)
	}
	f := view.View{Snapshot: snap, Now: now}.Fleet()
	for id, want := range map[string][2]bool{"vless-transit": {false, true}, "clash-mac": {true, false}, "backup-job": {false, false}} {
		card, _ := f.ServiceCard(id)
		if card.Monitored != want[0] || card.Elsewhere != want[1] {
			t.Fatalf("%s: monitored %v, elsewhere %v", id, card.Monitored, card.Elsewhere)
		}
	}
	if card, _ := previewView(t, "").Fleet().ServiceCard("vless-transit"); card.Elsewhere {
		t.Fatal("the whole view puts a service elsewhere")
	}
}
