package preview

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
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
	want := []string{"win", "mac", "router", "hz", "dmit", "aaitr", "qqpr", "s1", "s2", "jp", "de"}
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
	for _, id := range []string{"s1-db", "jp-openlist"} {
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
	exit := check(t, s, "w-exit-dmit-aaitr")
	if exit.Health != "up" || exit.Last.Facts["exit_ip"] != "203.0.113.40" || exit.Last.Facts["response_proof"] != "verified_https" {
		t.Fatal(exit.Health, exit.Last.Facts, exit.Last.Detail)
	}
	clash := check(t, s, "w-clash")
	if !strings.Contains(clash.Last.Facts["reported_chain"], "DMIT") {
		t.Fatal("primary chain not selected", clash.Last.Facts["reported_chain"])
	}
}

func TestBlockedLineFallsBackAndKeepsCounterEvidence(t *testing.T) {
	s := settled(t, "dmit-blocked")
	if check(t, s, "w-dmit-443").Health != "down" || check(t, s, "w-dmit-icmp").Health != "up" {
		t.Fatal("public port and icmp should disagree")
	}
	tn := check(t, s, "w-tn-dmit")
	if tn.Health != "up" || tn.Last.DurationMS < 50 {
		t.Fatal("tailnet should survive via relay", tn.Health, tn.Last)
	}
	ts := check(t, s, "w-ts-dmit")
	if ts.Health != "up" || ts.Last.Facts["relay"] != "hz" {
		t.Fatal("tailscale relay", ts.Last.Facts)
	}
	if check(t, s, "s-dmit-tn").Health != "up" || check(t, s, "w-exit-s2-aaitr").Health != "up" {
		t.Fatal("overseas contrast or fallback exit failed")
	}
	if check(t, s, "w-exit-dmit-aaitr").Health != "down" {
		t.Fatal("primary chain should fail")
	}
	clash := check(t, s, "w-clash")
	if !strings.Contains(clash.Last.Facts["reported_chain"], "S2") {
		t.Fatal("clash did not fall back", clash.Last.Facts["reported_chain"])
	}
	found := false
	for _, inc := range s.Incidents {
		for _, id := range inc.Checks {
			if id == "w-dmit-443" {
				found = true
				if inc.Hypothesis == "" || !contains(inc.CounterEvidence, "s-dmit-tn") {
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
	down := settled(t, "dmit-down")
	if check(t, down, "w-tn-dmit").Health != "down" || check(t, down, "s-dmit-tn").Health != "down" || check(t, down, "w-ts-dmit").Health != "down" {
		t.Fatal("a dead host should fail from every observer, including tailnet")
	}
	if check(t, down, "w-exit-s2-aaitr").Health != "up" {
		t.Fatal("fallback chain should stay up")
	}
	for _, inc := range down.Incidents {
		for _, id := range inc.Checks {
			if id == "w-dmit-443" && inc.Hypothesis != "" {
				t.Fatal("no contrast exists when every observer fails", inc)
			}
		}
	}
	exit := settled(t, "aaitr-down")
	if check(t, exit, "w-exit-dmit-aaitr").Health != "down" || check(t, exit, "w-exit-dmit-qqpr").Health != "up" {
		t.Fatal("only the AAITR exit should fail")
	}
	if entity(t, exit.Paths, "w-egress").Health != "degraded" {
		t.Fatal("candidate set should degrade, not go down")
	}
	if !strings.Contains(check(t, exit, "w-clash").Last.Facts["reported_chain"], "QQPR") {
		t.Fatal("clash should move to the Hawaii exit")
	}
}

func TestPolicyDriftMaintenanceOutageAndClock(t *testing.T) {
	drift := settled(t, "aaitr-exit-drift")
	bad := check(t, drift, "w-exit-dmit-aaitr")
	if bad.Health != "degraded" || bad.Last.Outcome != "policy" || bad.Last.Facts["exit_ip"] != "203.0.113.99" {
		t.Fatal(bad.Health, bad.Last)
	}
	if check(t, drift, "w-exit-dmit-qqpr").Health != "up" {
		t.Fatal("the other exit is unaffected")
	}
	maint := settled(t, "s2-maintenance")
	if check(t, maint, "w-s2-443").Freshness != "maintenance" || check(t, maint, "w-memos").Freshness != "maintenance" {
		t.Fatal("s2 checks should leave alerting")
	}
	if check(t, maint, "w-exit-dmit-aaitr").Health != "up" {
		t.Fatal("unrelated checks changed")
	}
	for _, inc := range maint.Incidents {
		for _, id := range inc.Checks {
			if strings.Contains(id, "s2") {
				t.Fatal("maintenance created an incident", inc)
			}
		}
	}
	home := settled(t, "home-offline")
	if check(t, home, "w-tn-s1").Freshness != "never" || check(t, home, "m-tn-win").Freshness != "never" {
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
	if check(t, hz, "h-derp").Freshness != "never" || check(t, hz, "j-home").Health != "down" {
		t.Fatal("hangzhou observer and the relay should both be gone")
	}
	clock := settled(t, "tokyo-clock-skew")
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

func TestEdgeAnswersForTheOriginBehindIt(t *testing.T) {
	ok := settled(t, "normal")
	if f := check(t, ok, "h-s1-web").Last.Facts; f["edge_provider"] != "cloudflare" || f["edge_pop"] != "LAX" || f["edge_cache"] != "DYNAMIC" {
		t.Fatal("mainland visitors should land on a US edge", f)
	}
	if f := check(t, ok, "j-blog").Last.Facts; f["edge_pop"] != "NRT" {
		t.Fatal("Tokyo visitors should land in Tokyo", f)
	}
	if f := check(t, ok, "j-docs").Last.Facts; f["edge_cache"] != "HIT" {
		t.Fatal("Pages are served from the edge cache", f)
	}
	if f := check(t, ok, "j-webapp").Last.Facts; f["edge_provider"] != "vercel" || f["edge_region"] != "hnd1" {
		t.Fatal("Vercel evidence", f)
	}
	for _, id := range []string{"cf", "s1", "vercel", "supabase"} {
		if n := entity(t, ok.Nodes, id); n.Health != "up" {
			t.Fatal("healthy edge or origin without evidence", n)
		}
	}

	down := settled(t, "s1-origin-down")
	blog := check(t, down, "j-blog")
	if blog.Health != "down" || blog.Last.Facts["http_status"] != "521" || !blog.Last.Connectivity {
		t.Fatal("the edge should answer for a dead origin", blog.Health, blog.Last)
	}
	if cf := entity(t, down.Nodes, "cf"); cf.Health != "up" || cf.Affected {
		t.Fatal("the edge answered and is not at fault", cf)
	}
	if s1 := entity(t, down.Nodes, "s1"); s1.Health == "up" || !s1.Affected {
		t.Fatal("an edge error page counted as the origin answering", s1)
	}
	if check(t, down, "j-worker").Health != "up" || check(t, down, "j-webapp").Health != "up" {
		t.Fatal("what runs on the edge does not depend on S1")
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
	if h := check(t, blocked, "h-worker"); h.Health != "down" || h.Last.Connectivity {
		t.Fatal("mainland requests should get no answer at all", h.Health, h.Last)
	}
	if check(t, blocked, "j-worker").Health != "up" || check(t, blocked, "j-blog").Health != "up" {
		t.Fatal("the edge still answers elsewhere")
	}
	for _, inc := range blocked.Incidents {
		if contains(inc.Checks, "h-worker") && !contains(inc.CounterEvidence, "j-worker") {
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
	mine, _ := s.Snapshot("cs-win", now)
	for _, r := range mine.Ledger {
		if !strings.HasPrefix(r.Scope, "cs-win/") {
			t.Fatalf("cs-win's view carries %s", r.Scope)
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
