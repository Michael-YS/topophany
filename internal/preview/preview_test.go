package preview

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func settled(t *testing.T, id string) model.Snapshot {
	t.Helper()
	return settledIn(t, id, DefaultLanguage)
}

// settledIn is the snapshot of a scenario half an hour after it began, with
// the built-in topology written in lang.
func settledIn(t *testing.T, id, lang string) model.Snapshot {
	t.Helper()
	c, e := Preset(lang)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	s := New(c, now, lang)
	i := slices.Index(s.ScenarioIDs(), id)
	if i < 0 {
		t.Fatal("missing scenario", id)
	}
	s.SetScenario(i, now.Add(-30*time.Minute))
	snap, e := s.Snapshot("", now)
	if e != nil {
		t.Fatal(id, e)
	}
	return snap
}

func check(t *testing.T, s model.Snapshot, id string) model.CheckState {
	t.Helper()
	for _, c := range s.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatal("missing check", id)
	return model.CheckState{}
}
func entity(t *testing.T, list []model.EntityState, id string) model.EntityState {
	t.Helper()
	for _, e := range list {
		if e.ID == id {
			return e
		}
	}
	t.Fatal("missing entity", id)
	return model.EntityState{}
}

func TestPresetModelsTheDeclaredNetwork(t *testing.T) {
	c, e := Preset(DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"win", "mac", "router", "relay", "transit", "exit-a", "exit-b", "app", "aux", "jp", "git-node"}
	for _, id := range want {
		found := false
		for _, n := range c.Nodes {
			if n.ID == id && n.Location != nil {
				found = true
			}
		}
		if !found {
			t.Fatal("node without a declared location", id)
		}
	}
	for _, id := range []string{"app-db", "jp-openlist"} {
		var n model.Node
		for _, x := range c.Nodes {
			if x.ID == id {
				n = x
			}
		}
		if n.Parent == "" || n.Location != nil {
			t.Fatal("container should inherit its parent's location", id)
		}
	}
	if len(c.Checks) < 40 || len(c.Paths) < 25 {
		t.Fatal("preset is not the complex topology", len(c.Checks), len(c.Paths))
	}
}

func TestHealthyPresetMostlyUpWithLiveObservers(t *testing.T) {
	s := settled(t, "normal")
	if s.Instance != "preview" || s.Project != "preview-homelab" {
		t.Fatal(s.Instance, s.Project)
	}
	down := 0
	for _, c := range s.Checks {
		if c.Health == "down" {
			down++
		}
		if c.Freshness != "fresh" {
			t.Fatal(c.ID, c.Freshness, c.Reason)
		}
	}
	if down > 0 {
		t.Fatal("healthy scenario confirmed a failure", down)
	}
	for _, o := range s.Observers {
		if o.Health != "up" {
			t.Fatal("observer", o)
		}
	}
	exit := check(t, s, "w-exit-transit-a")
	if exit.Health != "up" || exit.Last.Facts["exit_ip"] != "203.0.113.40" || exit.Last.Facts["response_proof"] != "verified_https" {
		t.Fatal(exit.Health, exit.Last.Facts, exit.Last.Detail)
	}
	clash := check(t, s, "w-clash")
	if !strings.Contains(clash.Last.Facts["reported_chain"], "BandwagonHost") {
		t.Fatal("primary chain not selected", clash.Last.Facts["reported_chain"])
	}
}

func TestBlockedLineFallsBackAndKeepsCounterEvidence(t *testing.T) {
	s := settled(t, "transit-blocked")
	if check(t, s, "w-transit-443").Health != "down" || check(t, s, "w-transit-icmp").Health != "up" {
		t.Fatal("public port and icmp should disagree")
	}
	tn := check(t, s, "w-tn-transit")
	if tn.Health != "up" || tn.Last.DurationMS < 30 {
		t.Fatal("tailnet should survive via relay", tn.Health, tn.Last)
	}
	ts := check(t, s, "w-ts-transit")
	if ts.Health != "up" || ts.Last.Facts["relay"] != "relay" {
		t.Fatal("tailscale relay", ts.Last.Facts)
	}
	if check(t, s, "s-transit-tn").Health != "up" || check(t, s, "w-exit-aux-a").Health != "up" {
		t.Fatal("overseas contrast or fallback exit failed")
	}
	if check(t, s, "w-exit-transit-a").Health != "down" {
		t.Fatal("primary chain should fail")
	}
	clash := check(t, s, "w-clash")
	if !strings.Contains(clash.Last.Facts["reported_chain"], "Azure") {
		t.Fatal("clash did not fall back", clash.Last.Facts["reported_chain"])
	}
	found := false
	for _, inc := range s.Incidents {
		for _, id := range inc.Checks {
			if id == "w-transit-443" {
				found = true
				if inc.Hypothesis == "" || !contains(inc.CounterEvidence, "s-transit-tn") {
					t.Fatal("missing counter-evidence", inc)
				}
			}
		}
	}
	if !found {
		t.Fatal("no incident for the blocked entry")
	}
}

func TestHostDownIsGlobalAndExitFailureIsSpecific(t *testing.T) {
	down := settled(t, "transit-down")
	if check(t, down, "w-tn-transit").Health != "down" || check(t, down, "s-transit-tn").Health != "down" || check(t, down, "w-ts-transit").Health != "down" {
		t.Fatal("a dead host should fail from every observer, including tailnet")
	}
	if check(t, down, "w-exit-aux-a").Health != "up" {
		t.Fatal("fallback chain should stay up")
	}
	for _, inc := range down.Incidents {
		for _, id := range inc.Checks {
			if id == "w-transit-443" && inc.Hypothesis != "" {
				t.Fatal("no contrast exists when every observer fails", inc)
			}
		}
	}
	exit := settled(t, "exit-down")
	if check(t, exit, "w-exit-transit-a").Health != "down" || check(t, exit, "w-exit-transit-b").Health != "up" {
		t.Fatal("only the exit-a should fail")
	}
	if entity(t, exit.Paths, "w-egress").Health != "degraded" {
		t.Fatal("candidate set should degrade, not go down")
	}
	if !strings.Contains(check(t, exit, "w-clash").Last.Facts["reported_chain"], "Vultr") {
		t.Fatal("clash should move to the Hawaii exit")
	}
}

func TestPolicyDriftMaintenanceOutageAndClock(t *testing.T) {
	drift := settled(t, "exit-drift")
	bad := check(t, drift, "w-exit-transit-a")
	if bad.Health != "degraded" || bad.Last.Outcome != "policy" || bad.Last.Facts["exit_ip"] != "203.0.113.99" {
		t.Fatal(bad.Health, bad.Last)
	}
	if check(t, drift, "w-exit-transit-b").Health != "up" {
		t.Fatal("the other exit is unaffected")
	}
	maint := settled(t, "aux-maintenance")
	if check(t, maint, "w-aux-443").Freshness != "maintenance" || check(t, maint, "w-vaultwarden").Freshness != "maintenance" {
		t.Fatal("aux checks should leave alerting")
	}
	if check(t, maint, "w-exit-transit-a").Health != "up" {
		t.Fatal("unrelated checks changed")
	}
	for _, inc := range maint.Incidents {
		for _, id := range inc.Checks {
			if strings.Contains(id, "aux") {
				t.Fatal("maintenance created an incident", inc)
			}
		}
	}
	home := settled(t, "home-offline")
	if check(t, home, "w-tn-app").Freshness != "never" || check(t, home, "m-tn-win").Freshness != "never" {
		t.Fatal("offline workers should not keep fresh results")
	}
	if check(t, home, "s-web-local").Health != "up" {
		t.Fatal("a local outage marked remote hosts down")
	}
	proxy := check(t, home, "j-home")
	if proxy.Health != "down" || proxy.Last.Facts["http_status"] != "502" {
		t.Fatal("visitor should see the reverse proxy fail", proxy.Health, proxy.Last)
	}
	hz := settled(t, "relay-down")
	if check(t, hz, "r-derp").Freshness != "never" || check(t, hz, "j-home").Health != "down" {
		t.Fatal("relay observer and the relay should both be gone")
	}
	clock := settled(t, "remote-clock-skew")
	jp := check(t, clock, "j-openlist")
	if jp.Health != "unknown" || jp.Freshness != "stale" || !strings.Contains(jp.Reason, "clock") {
		t.Fatal(jp)
	}
	if check(t, clock, "s-web-local").Health != "up" {
		t.Fatal("clock skew spread to other observers")
	}
}

func TestUnreachableAndForeignTopology(t *testing.T) {
	c, e := Preset(DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	s := New(c, now, DefaultLanguage)
	for i, id := range s.ScenarioIDs() {
		if id == "collector-unreachable" {
			s.SetScenario(i, now.Add(-time.Minute))
		}
	}
	if _, e = s.Snapshot("", now); e == nil || !strings.Contains(e.Error(), "collector") {
		t.Fatal(e)
	}
	small := model.Config{Schema: 1, Project: "lab", Nodes: []model.Node{{ID: "box", Name: "Box"}}, Observers: []model.Observer{{ID: "here", Name: "Here", Node: "box", Context: "local", StaleSeconds: 30}}}
	sim := New(small, now, DefaultLanguage)
	if len(sim.Scenarios()) >= len(scenarios) {
		t.Fatal("scenarios that name missing nodes stayed available", len(sim.Scenarios()))
	}
	for _, id := range sim.ScenarioIDs() {
		if id != "normal" && id != "cross-border-loss" && id != "chaos" && id != "collector-unreachable" {
			t.Fatal("inapplicable scenario", id)
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestEdgePopUsesCloudflaresOwnLocations(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ lang, observer, provider, want string }{
		{"en", "obs-win", "cloudflare", "SEA"},
		{"en", "obs-remote", "cloudflare", "FRA"},
		{"ja", "obs-win", "cloudflare", "KIX"},
		{"zh", "obs-win", "cloudflare", "LAX"}, // a mainland observer is carried abroad
		{"en", "obs-win", "vercel", "sfo1"},
		{"zh", "obs-win", "vercel", "hkg1"},
	} {
		c, err := Preset(tc.lang)
		if err != nil {
			t.Fatal(err)
		}
		s := New(c, now, tc.lang)
		code, loc := s.edgePop(tc.provider, tc.observer)
		if code != tc.want {
			t.Fatalf("%s/%s from %s = %q, want %q", tc.lang, tc.provider, tc.observer, code, tc.want)
		}
		if tc.provider != "cloudflare" {
			continue
		}
		known, ok := view.ColoAt(code)
		if !ok {
			t.Fatalf("%s is not in Cloudflare's generated locations", code)
		}
		if loc == nil || loc.Label != code || loc.Lat != known.Lat || loc.Lon != known.Lon {
			t.Fatalf("%s: location %+v does not match the table's %+v", code, loc, known)
		}
	}
	c, err := Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	if code, loc := New(c, now, "en").edgePop("netlify", "obs-win"); code != "" || loc != nil {
		t.Fatalf("an edge with no location list returned %q, %+v", code, loc)
	}
}

func TestEdgeAnswersForTheOriginBehindIt(t *testing.T) {
	ok := settled(t, "normal")
	if f := check(t, ok, "r-app-web").Last.Facts; f["edge_provider"] != "cloudflare" || f["edge_cache"] != "DYNAMIC" {
		t.Fatal("relay visitors should land on a Cloudflare edge", f)
	}
	if f := check(t, ok, "j-blog").Last.Facts; f["edge_provider"] != "cloudflare" || f["edge_pop"] == "" {
		t.Fatal("remote visitors should land on edge", f)
	}
	if f := check(t, ok, "j-docs").Last.Facts; f["edge_cache"] != "HIT" {
		t.Fatal("Pages are served from the edge cache", f)
	}
	if f := check(t, ok, "j-webapp").Last.Facts; f["edge_provider"] != "vercel" || !strings.HasSuffix(f["vercel_route_regions"], ":hnd1") || f["vercel_edge_region"] == "" || f["edge_pop"] != "" {
		t.Fatal("Vercel evidence", f)
	}
	for _, id := range []string{"cf", "app", "vercel", "supabase"} {
		if n := entity(t, ok.Nodes, id); n.Health != "up" {
			t.Fatal("healthy edge or origin without evidence", n)
		}
	}

	down := settled(t, "app-origin-down")
	blog := check(t, down, "j-blog")
	if blog.Health != "down" || blog.Last.Facts["http_status"] != "521" || !blog.Last.Connectivity {
		t.Fatal("the edge should answer for a dead origin", blog.Health, blog.Last)
	}
	if cf := entity(t, down.Nodes, "cf"); cf.Health != "up" || cf.Affected {
		t.Fatal("the edge answered and is not at fault", cf)
	}
	if app := entity(t, down.Nodes, "app"); app.Health == "up" || !app.Affected {
		t.Fatal("an edge error page counted as the origin answering", app)
	}
	if check(t, down, "j-worker").Health != "up" || check(t, down, "j-webapp").Health != "up" {
		t.Fatal("what runs on the edge does not depend on app")
	}
	explained := false
	for _, inc := range down.Incidents {
		if contains(inc.Checks, "j-blog") {
			explained = strings.Contains(inc.Hypothesis, "edge in front answered with HTTP 521")
		}
	}
	if !explained {
		t.Fatal("no bounded diagnosis behind the edge", down.Incidents)
	}

	blocked := settled(t, "edge-blocked")
	if h := check(t, blocked, "r-worker"); h.Health != "down" || h.Last.Connectivity {
		t.Fatal("blocked requests should get no answer at all", h.Health, h.Last)
	}
	if check(t, blocked, "j-worker").Health != "up" || check(t, blocked, "j-blog").Health != "up" {
		t.Fatal("the edge still answers elsewhere")
	}
	for _, inc := range blocked.Incidents {
		if contains(inc.Checks, "r-worker") && !contains(inc.CounterEvidence, "j-worker") {
			t.Fatal("Tokyo's answers are counter-evidence", inc)
		}
	}
}

// The preview remembers incidents as a collector watching all along would:
// a scenario's incidents open in the minutes after it begins, close once
// it ends, and a simulator started afresh on the same timeline remembers
// the same. A view of one observer carries only its own records.
func TestPreviewRemembersIncidents(t *testing.T) {
	c, e := Preset(DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	began := now.Add(-30 * time.Minute)
	s := New(c, now, DefaultLanguage)
	s.SetScenario(2, began)
	snap, e := s.Snapshot("", now)
	if e != nil || len(snap.Incidents) == 0 || len(snap.Ledger) != len(snap.Incidents) {
		t.Fatalf("%d incidents, %d records: %v", len(snap.Incidents), len(snap.Ledger), e)
	}
	for _, r := range snap.Ledger {
		if r.State != "open" || r.Opened.Before(began) || r.Opened.After(began.Add(3*time.Minute)) || r.Since.After(r.Opened) {
			t.Fatalf("record %+v of an outage that began at %s", r, began)
		}
	}
	fresh := New(c, now, DefaultLanguage)
	fresh.SetScenario(2, began)
	again, _ := fresh.Snapshot("", now)
	if len(again.Ledger) != len(snap.Ledger) || again.Ledger[0].ID != snap.Ledger[0].ID || !again.Ledger[0].Opened.Equal(snap.Ledger[0].Opened) {
		t.Fatal("a simulator on the same timeline remembers otherwise")
	}
	mine, _ := s.Snapshot("obs-win", now)
	for _, r := range mine.Ledger {
		if !strings.HasPrefix(r.Scope, "obs-win/") {
			t.Fatalf("obs-win's view carries %s", r.Scope)
		}
	}
	s.SetScenario(0, now)
	later, _ := s.Snapshot("", now.Add(10*time.Minute))
	if len(later.Incidents) != 0 || len(later.Ledger) != len(snap.Ledger) {
		t.Fatalf("after the outage: %d incidents, %d records", len(later.Incidents), len(later.Ledger))
	}
	for _, r := range later.Ledger {
		if r.State != "closed" || r.Ended != "recovered" {
			t.Fatalf("after the outage %+v", r)
		}
	}
}

// The preview keeps rollups as a collector watching all along would: a
// normal month is measured throughout, a check put in maintenance or an
// observer gone quiet leaves time unmeasured rather than up, and a
// scenario changes how the checks it touches fared and no others.
func TestPreviewKeepsRollups(t *testing.T) {
	calm := settled(t, "normal")
	for _, c := range calm.Checks {
		if len(c.Availability) != 3 || c.Availability[2].Unmeasured != 0 || c.Availability[2].Seconds != 30*86400 {
			t.Fatalf("%s: a normal month not measured throughout: %+v", c.ID, c.Availability)
		}
	}
	for scenario, id := range map[string]string{"aux-maintenance": "w-aux-443", "home-offline": "w-tn-app"} {
		day := check(t, settled(t, scenario), id).Availability[0]
		if day.Unmeasured < 25*60 || day.Unmeasured > 30*60 {
			t.Fatalf("%s: %s's last half hour reads as measured: %+v", scenario, id, day.Rollup)
		}
	}
	down := settled(t, "transit-down")
	for _, c := range calm.Checks {
		if !strings.Contains(c.ID, "transit") && !same(check(t, down, c.ID).Availability, c.Availability) {
			t.Fatalf("transit down changed how %s fared", c.ID)
		}
	}
	if d := check(t, down, "w-transit-443").Availability[0].Down - check(t, calm, "w-transit-443").Availability[0].Down; d < 25*60 || d > 30*60 {
		t.Fatalf("half an hour of transit down adds %ds down", d)
	}
}

func same(a, b []model.Availability) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Rollup.Up != b[i].Rollup.Up || a[i].Down != b[i].Down || a[i].Unmeasured != b[i].Unmeasured || a[i].Samples != b[i].Samples {
			return false
		}
	}
	return true
}

// A cloudflared check in a preview topology is a reported local connector
// read, never a measured request: the link it feeds is up, and the service
// behind the tunnel stays unproven.
func TestCloudflaredSimulationIsReported(t *testing.T) {
	c := testkit.Config()
	expected := 4
	c.Checks = append(c.Checks, model.Check{
		ID: "tunnel-connector", Name: "cloudflared", Observer: "home", Link: "home-dmit-link",
		Kind: "cloudflared", Target: "http://127.0.0.1:20241", IntervalSeconds: 60,
		TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2,
		Adapter: model.AdapterOptions{Cloudflared: &model.CloudflaredOptions{ExpectedConnections: &expected}},
	})
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	s := New(c, now, DefaultLanguage)
	snap, err := s.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	state := check(t, snap, "tunnel-connector")
	if state.Health != "up" || state.Last == nil {
		t.Fatalf("check %+v", state)
	}
	if state.Last.Evidence != "reported" || state.Last.PathEvidence != model.LocalConnectorPathEvidence || state.Last.Connectivity {
		t.Fatalf("cloudflared simulated as a measured request: %+v", state.Last)
	}
	if state.Last.Facts["ready_connections"] != "4" || state.Last.Facts["connection_colos"] != "hkg,sin" {
		t.Fatalf("connector facts %v", state.Last.Facts)
	}
	link := entity(t, snap.Links, "home-dmit-link")
	if link.Health != "up" {
		t.Fatalf("link %+v", link)
	}
}

func TestPreviewMapPlacesTheColoTheEdgeReported(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	for lang, want := range map[string]map[string]string{
		"en": {"SJC": "San Jose", "EWR": "Newark", "FRA": "Frankfurt-am-Main"},
		"ja": {"KIX": "Osaka", "EWR": "Newark", "FRA": "Frankfurt-am-Main"},
		"zh": {"LAX": "Los Angeles", "EWR": "Newark", "FRA": "Frankfurt-am-Main"},
	} {
		c, err := Preset(lang)
		if err != nil {
			t.Fatal(err)
		}
		snap, err := New(c, now, lang).Snapshot("", now)
		if err != nil {
			t.Fatal(err)
		}
		nodes, edges := (view.View{Snapshot: snap, Now: now}).Graph()
		found := map[string]view.MapNode{}
		for _, n := range nodes {
			if n.Kind == "colo" {
				found[n.ID] = n
			}
		}
		if len(found) != len(want) {
			t.Fatalf("%s: colo nodes = %v, want %v", lang, found, want)
		}
		for code, place := range want {
			id := "colo:cf:" + code
			n, ok := found[id]
			if !ok {
				t.Fatalf("%s: no %s", lang, id)
			}
			known, knownOK := view.ColoAt(code)
			if !knownOK || !n.Located || n.Place != place || n.Lat != known.Lat || n.Lon != known.Lon {
				t.Fatalf("%s: %s placed at %q %.4f,%.4f, want %q %.4f,%.4f", lang, id, n.Place, n.Lat, n.Lon, place, known.Lat, known.Lon)
			}
			if view.NodeSignal(n) != "up" {
				t.Fatalf("%s: %s carries a passing preview check but reads %q", lang, id, view.NodeSignal(n))
			}
			hops := 0
			for _, e := range edges {
				if e.Kind != view.EdgeColo {
					continue
				}
				if e.To == id {
					hops++
					if e.From == "" || e.From == "cf" {
						t.Fatalf("%s: observer hop from %q", lang, e.From)
					}
				}
				if e.From == id && e.To != "cf" {
					t.Fatalf("%s: %s fronts %q, not the edge", lang, id, e.To)
				}
			}
			if hops != 1 {
				t.Fatalf("%s: %s has %d observer hops, want 1", lang, id, hops)
			}
		}
	}
}
