package preview

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func settled(t *testing.T, name string) model.Snapshot {
	t.Helper()
	c, e := Preset()
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	s := New(c, now)
	for i, line := range s.Scenarios() {
		if strings.HasPrefix(line, name+"\t") || line == name {
			s.SetScenario(i, now.Add(-30*time.Minute))
			snap, e := s.Snapshot("", now)
			if e != nil {
				t.Fatal(name, e)
			}
			return snap
		}
	}
	t.Fatal("missing scenario", name)
	return model.Snapshot{}
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
	c, e := Preset()
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
	s := settled(t, "全部正常")
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
	s := settled(t, "DMIT 线路被阻断（仅国内）")
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
	down := settled(t, "DMIT 线路机宕机")
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
	exit := settled(t, "AAITR 家宽落地故障")
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
	drift := settled(t, "AAITR 出口 IP 漂移")
	bad := check(t, drift, "w-exit-dmit-aaitr")
	if bad.Health != "degraded" || bad.Last.Outcome != "policy" || bad.Last.Facts["exit_ip"] != "203.0.113.99" {
		t.Fatal(bad.Health, bad.Last)
	}
	if check(t, drift, "w-exit-dmit-qqpr").Health != "up" {
		t.Fatal("the other exit is unaffected")
	}
	maint := settled(t, "S2 维护窗口")
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
	home := settled(t, "长沙家宽断网")
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
	hz := settled(t, "杭州中继宕机")
	if check(t, hz, "h-derp").Freshness != "never" || check(t, hz, "j-home").Health != "down" {
		t.Fatal("hangzhou observer and the relay should both be gone")
	}
	clock := settled(t, "东京 worker 时钟超前")
	jp := check(t, clock, "j-openlist")
	if jp.Health != "unknown" || jp.Freshness != "stale" || !strings.Contains(jp.Reason, "clock") {
		t.Fatal(jp)
	}
	if check(t, clock, "s-web-local").Health != "up" {
		t.Fatal("clock skew spread to other observers")
	}
}

func TestUnreachableAndForeignTopology(t *testing.T) {
	c, e := Preset()
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	s := New(c, now)
	for i, line := range s.Scenarios() {
		if strings.HasPrefix(line, "Collector") {
			s.SetScenario(i, now.Add(-time.Minute))
		}
	}
	if _, e = s.Snapshot("", now); e == nil || !strings.Contains(e.Error(), "collector") {
		t.Fatal(e)
	}
	small := model.Config{Schema: 1, Project: "lab", Nodes: []model.Node{{ID: "box", Name: "Box"}}, Observers: []model.Observer{{ID: "here", Name: "Here", Node: "box", Context: "local", StaleSeconds: 30}}}
	sim := New(small, now)
	if len(sim.Scenarios()) >= len(scenarios) {
		t.Fatal("scenarios that name missing nodes stayed available", len(sim.Scenarios()))
	}
	for _, line := range sim.Scenarios() {
		if strings.Contains(line, "DMIT") || strings.Contains(line, "长沙") {
			t.Fatal("inapplicable scenario", line)
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
