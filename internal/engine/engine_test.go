package engine

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"strings"
	"testing"
	"time"
)

func TestConfirmationRecoveryFreshnessAndMonitorErrors(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	now := time.Now().UTC()
	tests := []struct {
		name     string
		outcomes []string
		want     string
	}{{"first response", []string{"ok"}, "up"}, {"suspected failure", []string{"fail"}, "degraded"}, {"confirmed failure", []string{"fail", "fail", "fail"}, "down"}, {"recovering", []string{"fail", "fail", "fail", "ok"}, "degraded"}, {"recovered", []string{"fail", "fail", "fail", "ok", "ok"}, "up"}, {"monitor permission error", []string{"fail", "fail", "error"}, "unknown"}, {"unknown clears confirmation", []string{"fail", "fail", "unknown", "fail"}, "degraded"}, {"wrong exit is policy, not outage", []string{"policy"}, "degraded"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := []model.Observation{}
			for i, v := range tt.outcomes {
				rows = append(rows, testkit.Observation(c, x.ID, uint64(i+1), now.Add(time.Duration(i-len(tt.outcomes))*time.Second), v))
			}
			got := Evaluate(x, rows, now)
			if got.Health != tt.want {
				t.Fatalf("want %s, got %+v", tt.want, got)
			}
		})
	}
	old := testkit.Observation(c, x.ID, 1, now.Add(-time.Hour), "fail")
	s := Evaluate(x, []model.Observation{old}, now)
	if s.Health != "unknown" || s.Freshness != "stale" || s.Failures != 0 {
		t.Fatal("old sample marked current", s)
	}
	future := old
	future.SampledAt = now.Add(time.Minute)
	if s = Evaluate(x, []model.Observation{future}, now); s.Health != "unknown" || !strings.Contains(s.Reason, "clock") {
		t.Fatal("future sample treated fresh", s)
	}
	if s = Evaluate(x, nil, now); s.Freshness != "never" {
		t.Fatal(s)
	}
	x.Disabled = true
	if s = Evaluate(x, []model.Observation{old}, now); s.Freshness != "paused" || s.Last != nil {
		t.Fatal(s)
	}
	x.Disabled = false
	x.Maintenance = true
	if s = Evaluate(x, []model.Observation{old}, now); s.Freshness != "maintenance" {
		t.Fatal(s)
	}
}
func TestOutOfOrderAndGapDoNotFabricateConfirmation(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	now := time.Now()
	rows := []model.Observation{testkit.Observation(c, x.ID, 3, now.Add(-time.Second), "ok"), testkit.Observation(c, x.ID, 1, now.Add(-time.Hour), "fail"), testkit.Observation(c, x.ID, 2, now.Add(-time.Hour+time.Second), "fail")}
	s := Evaluate(x, rows, now)
	if s.Health != "up" || s.Last.Sequence != 3 {
		t.Fatal(s)
	}
	rows[0].Outcome = "fail"
	s = Evaluate(x, rows, now)
	if s.Health != "degraded" || s.Failures != 1 {
		t.Fatal("failures counted through expired monitoring gap", s)
	}
}
func seedFailures(c model.Config, id string, base uint64, now time.Time) []model.Observation {
	out := []model.Observation{}
	for i := 0; i < 3; i++ {
		out = append(out, testkit.Observation(c, id, base+uint64(i), now.Add(time.Duration(i-3)*time.Second), "fail"))
	}
	return out
}
func findEntity(t *testing.T, rows []model.EntityState, id string) model.EntityState {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatal("missing entity", id)
	return model.EntityState{}
}
func TestSourceScopedFilteringNotServerDowntime(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	rows := append(seedFailures(c, "home-http", 1, now), seedFailures(c, "home-ssh", 4, now)...)
	rows = append(rows, testkit.Observation(c, "cloud-http", 10, now.Add(-time.Second), "ok"))
	s := Build(c, "collector", "", rows, nil, now)
	n := findEntity(t, s.Nodes, "dmit")
	if n.Health != "up" || !n.Affected {
		t.Fatal("healthy contrast source was ignored", n)
	}
	if v := findEntity(t, s.Paths, "direct"); v.Health != "down" {
		t.Fatal(v)
	}
	if v := findEntity(t, s.Services, "web"); v.Health != "degraded" {
		t.Fatal("mixed access collapsed into global service outage", v)
	}
	if len(s.Incidents) != 1 {
		t.Fatalf("related direct-path failures should group: %+v", s.Incidents)
	}
	i := s.Incidents[0]
	if len(i.CounterEvidence) == 0 || !strings.Contains(i.Hypothesis, "not established") {
		t.Fatal("no bounded diagnosis", i)
	}
	if strings.Contains(i.Summary, "GFW") || strings.Contains(i.Summary, "all ports") {
		t.Fatal("overstated diagnosis")
	}
	home := Build(c, "a", "home", rows, nil, now)
	if v := findEntity(t, home.Nodes, "dmit"); v.Health == "down" {
		t.Fatal("home path failure called physical host down")
	}
	if len(home.Incidents[0].CounterEvidence) == 0 {
		t.Fatal("contrast evidence lost in observer view")
	}
}
func TestFallbackSelectionNotEndToEndRecovery(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	rows := seedFailures(c, "primary-http", 1, now)
	s := Build(c, "a", "home", rows, nil, now)
	policy := findEntity(t, s.Paths, "residential")
	if policy.Health == "up" {
		t.Fatal("unmeasured backup counted healthy")
	}
	rows = append(rows, testkit.Observation(c, "backup-http", 4, now, "ok"))
	s = Build(c, "a", "home", rows, nil, now)
	policy = findEntity(t, s.Paths, "residential")
	if policy.Health != "degraded" || !strings.Contains(policy.Reason, "actual failover not verified") {
		t.Fatal("candidate success confused with production recovery", policy)
	}
	if v := findEntity(t, s.Links, "dmit-exit-link"); v.Health != "unknown" || !v.Affected {
		t.Fatal("whole path failure falsely localized to segment", v)
	}
}
func TestServiceDependencyImpactAndRemarks(t *testing.T) {
	c := testkit.Config()
	c.Services[1].DependsOn = []string{"web"}
	now := time.Now()
	rows := seedFailures(c, "home-http", 1, now)
	s := Build(c, "a", "home", rows, nil, now)
	dsh := findEntity(t, s.Services, "dsh")
	if dsh.Health != "unknown" || !dsh.Affected {
		t.Fatal("dependency propagated as invented service failure", dsh)
	}
	notes := findEntity(t, s.Services, "notes")
	if notes.Health != "unknown" || len(notes.Checks) != 0 {
		t.Fatal("notes-only service counted healthy", notes)
	}
}
func TestTCPAcceptAndReportedTelemetryNotHostProof(t *testing.T) {
	for _, variant := range []string{"tcp-accept", "reported", "stale"} {
		t.Run(variant, func(t *testing.T) {
			c := testkit.Config()
			now := time.Now()
			v := testkit.Observation(c, "home-http", 1, now, "ok")
			switch variant {
			case "tcp-accept":
				v.Facts["response_proof"] = "none"
			case "reported":
				v.Evidence = "reported"
			case "stale":
				v.SampledAt = now.Add(-time.Hour)
			}
			s := Build(c, "a", "home", []model.Observation{v}, nil, now)
			if n := findEntity(t, s.Nodes, "dmit"); n.Health == "up" {
				t.Fatal("insufficient evidence upgraded to current node response", n)
			}
		})
	}
}
func TestHeartbeatDoesNotRefreshCheckAndForeignContractIgnored(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	v := testkit.Observation(c, "home-http", 1, now.Add(-time.Hour), "ok")
	beats := map[string]model.Heartbeat{"home": {Project: c.Project, Revision: config.Revision(c), Observer: "home", Boot: "new", SampledAt: now}}
	s := Build(c, "a", "home", []model.Observation{v}, beats, now)
	if s.Observers[0].Health != "up" || s.Checks[0].Freshness != "stale" {
		t.Fatal("heartbeat confused with fresh check")
	}
	v.Contract = "old-target-contract"
	v.SampledAt = now
	s = Build(c, "a", "home", []model.Observation{v}, beats, now)
	if s.Checks[0].Freshness != "never" {
		t.Fatal("old test URL reused in new measurement contract")
	}
	// A schema 1 sample counts only under the very revision it was taken with.
	v.Contract, v.Revision = "", config.Revision(c)
	if s = Build(c, "a", "home", []model.Observation{v}, beats, now); s.Checks[0].Freshness != "fresh" {
		t.Fatal("schema 1 sample of the current revision ignored")
	}
	v.Revision = strings.Repeat("0", 64)
	if s = Build(c, "a", "home", []model.Observation{v}, beats, now); s.Checks[0].Freshness != "never" {
		t.Fatal("schema 1 sample of another revision counted")
	}
	v.Contract, v.Revision = config.Contract(c, c.Checks[0]), ""
	v.Observer = "cloud"
	s = Build(c, "a", "home", []model.Observation{v}, nil, now)
	if s.Checks[0].Last != nil {
		t.Fatal("foreign observer result accepted")
	}
}

// A check's samples, its state and the incident it is part of survive any
// edit that changes nothing it measures, such as a note, a name, a place, a
// threshold or another check, and are left behind by one that does. The
// snapshot shows only public contracts.
func TestContractsSurviveEditsThatMeasureNothingNew(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	rows := []model.Observation{}
	for i := 0; i < 3; i++ {
		rows = append(rows, testkit.Observation(c, "home-http", uint64(i+1), now.Add(-time.Duration(3-i)*time.Second), "fail"))
	}
	before := Build(c, "a", "", rows, nil, now)
	if len(before.Incidents) == 0 {
		t.Fatal("fixture raised no incident")
	}
	state := func(s model.Snapshot) model.CheckState {
		for _, cs := range s.Checks {
			if cs.ID == "home-http" {
				return cs
			}
		}
		return model.CheckState{}
	}
	if cs := state(before); cs.Contract != config.PublicContracts(c)["home-http"] || cs.Last == nil || cs.Last.Contract != cs.Contract || cs.Last.Revision != "" || before.Revision != config.PublicRevision(c) {
		t.Fatal("snapshot does not show public digests only")
	}
	for name, edit := range map[string]func(*model.Config){
		"note":      func(c *model.Config) { c.Checks[0].Notes = "edited" },
		"name":      func(c *model.Config) { c.Checks[0].Name = "renamed"; c.Nodes[0].Name = "renamed" },
		"place":     func(c *model.Config) { c.Nodes[2].Location = &model.Location{Label: "LA", Lat: 34, Lon: -118} },
		"threshold": func(c *model.Config) { c.Checks[0].FailureThreshold = 2; c.Checks[0].IntervalSeconds = 30 },
		"other":     func(c *model.Config) { c.Checks[2].Target = "https://192.0.2.99/" }, // cloud-http, another scope
		"service": func(c *model.Config) {
			c.Services = append(c.Services, model.Service{ID: "new", Name: "New", Node: "s2"})
		},
	} {
		edited := testkit.Config()
		edit(&edited)
		after := Build(edited, "a", "", rows, nil, now)
		if cs := state(after); cs.Health != "down" || cs.Contract != state(before).Contract {
			t.Fatalf("%s edit reset the check: %s", name, cs.Health)
		}
		if len(after.Incidents) == 0 || after.Incidents[0].ID != before.Incidents[0].ID {
			t.Fatalf("%s edit renamed the incident", name)
		}
	}
	for name, edit := range map[string]func(*model.Config){
		"target":    func(c *model.Config) { c.Endpoints[0].Address = "https://192.0.2.11/" },
		"assertion": func(c *model.Config) { c.Checks[0].HTTP.Status = []int{204} },
		"timeout":   func(c *model.Config) { c.Checks[0].TimeoutSeconds++ },
		"proxy":     func(c *model.Config) { c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "192.0.2.5:1080"}} },
	} {
		edited := testkit.Config()
		edit(&edited)
		if cs := state(Build(edited, "a", "", rows, nil, now)); cs.Freshness != "never" || cs.Contract == state(before).Contract {
			t.Fatalf("%s edit kept samples of what was measured before", name)
		}
	}
}
func TestStoppedObserverIsUnknownNotMassOutage(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	rows := []model.Observation{testkit.Observation(c, "home-http", 1, now.Add(-time.Hour), "ok")}
	s := Build(c, "a", "home", rows, nil, now)
	if s.Observers[0].Health != "unknown" || len(s.Incidents) != 0 {
		t.Fatal("stopped executor created target outage incident")
	}
	for _, n := range s.Nodes {
		if n.Health == "down" {
			t.Fatal("stale executor marked node down", n)
		}
	}
}
func FuzzStateNeverPromotesExpiredSample(f *testing.F) {
	f.Add(int64(400), "ok")
	f.Add(int64(0), "fail")
	f.Fuzz(func(t *testing.T, seconds int64, outcome string) {
		if seconds < 181 || seconds > 1000000000 {
			return
		}
		c := testkit.Config()
		now := time.Unix(1900000000, 0)
		v := testkit.Observation(c, "home-http", 1, now.Add(-time.Duration(seconds)*time.Second), outcome)
		s := Evaluate(c.Checks[0], []model.Observation{v}, now)
		if s.Health != "unknown" || s.Freshness != "stale" {
			t.Fatal("expired observation became live", s)
		}
	})
}

func TestRuntimeSelectionAndTCPAcceptNeverProvePathRecovery(t *testing.T) {
	for _, kind := range []string{"runtime-selection", "tcp-accept"} {
		t.Run(kind, func(t *testing.T) {
			c := testkit.Config()
			now := time.Now()
			v := testkit.Observation(c, "primary-http", 1, now, "ok")
			if kind == "runtime-selection" {
				v.Evidence = "reported"
				v.Facts["reported_selection"] = "S2"
			} else {
				v.Facts["response_proof"] = "none"
			}
			s := Build(c, "a", "home", []model.Observation{v}, nil, now)
			if p := findEntity(t, s.Paths, "primary"); p.Health == "up" {
				t.Fatal("non-response evidence promoted to end-to-end recovery", p)
			}
		})
	}
}

// An address reached through an edge is answered by the edge first: its
// answer, even an error page, proves the edge; the origin behind it counts
// only a passing response that the edge did not serve from its cache.
func TestEdgeAnswersDoNotProveTheOrigin(t *testing.T) {
	edged := func() model.Config {
		c := testkit.Config()
		c.Nodes = append(c.Nodes, model.Node{ID: "edge", Name: "Edge", Kind: "edge", Provider: "cloudflare"})
		for i := range c.Endpoints {
			if c.Endpoints[i].ID == "dmit-http" {
				c.Endpoints[i].Via = []string{"edge"}
			}
		}
		return c
	}
	through := func(c model.Config, id string, seq uint64, at time.Time, outcome, status, cache string, answered bool) model.Observation {
		v := testkit.Observation(c, id, seq, at, outcome)
		v.Connectivity = answered
		v.Facts = map[string]string{}
		if answered {
			v.Facts = map[string]string{"response_proof": "verified_https", "http_status": status, "edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": cache}
		}
		return v
	}
	now := time.Now()
	c := edged()
	rows := []model.Observation{}
	for i := 0; i < 3; i++ {
		rows = append(rows, through(c, "home-http", uint64(i+1), now.Add(time.Duration(i-3)*time.Second), "fail", "521", "DYNAMIC", true))
	}
	s := Build(c, "a", "home", rows, nil, now)
	if e := findEntity(t, s.Nodes, "edge"); e.Health != "up" || e.Affected {
		t.Fatal("the edge answered and was not at fault", e)
	}
	if o := findEntity(t, s.Nodes, "dmit"); o.Health == "up" || !o.Affected {
		t.Fatal("an edge error page counted as the origin answering", o)
	}
	if len(s.Incidents) != 1 || !strings.Contains(s.Incidents[0].Hypothesis, "edge in front answered with HTTP 521") || !strings.Contains(s.Incidents[0].Hypothesis, "not established") {
		t.Fatal("no bounded diagnosis behind the edge", s.Incidents)
	}

	for _, tt := range []struct {
		cache  string
		origin string
	}{{"DYNAMIC", "up"}, {"MISS", "up"}, {"", "up"}, {"HIT", "unknown"}, {"STALE", "unknown"}} {
		v := through(c, "cloud-http", 1, now.Add(-time.Second), "ok", "200", tt.cache, true)
		s := Build(c, "a", "cloud", []model.Observation{v}, nil, now)
		if e := findEntity(t, s.Nodes, "edge"); e.Health != "up" {
			t.Fatal("edge answer not counted", tt.cache, e)
		}
		if o := findEntity(t, s.Nodes, "dmit"); o.Health != tt.origin {
			t.Fatalf("cache %q: origin %s, want %s", tt.cache, o.Health, tt.origin)
		}
	}

	// Nothing answered: the fault may be at the edge or anywhere behind it.
	rows = rows[:0]
	for i := 0; i < 3; i++ {
		rows = append(rows, through(c, "home-http", uint64(i+1), now.Add(time.Duration(i-3)*time.Second), "fail", "", "", false))
	}
	s = Build(c, "a", "home", rows, nil, now)
	if e, o := findEntity(t, s.Nodes, "edge"), findEntity(t, s.Nodes, "dmit"); e.Health == "up" || !e.Affected || !o.Affected {
		t.Fatal("unanswered requests cleared a hop", e, o)
	}
	if strings.Contains(s.Incidents[0].Hypothesis, "edge in front answered") {
		t.Fatal("claimed an answer that never came", s.Incidents[0])
	}
}
