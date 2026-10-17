package engine

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"slices"
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
func TestPolicyViolationNeedsTheFailureThreshold(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	now := time.Now().UTC()
	samples := func(outcomes ...string) []model.Observation {
		rows := make([]model.Observation, 0, len(outcomes))
		for i, outcome := range outcomes {
			rows = append(rows, testkit.Observation(c, x.ID, uint64(i+1), now.Add(time.Duration(i-len(outcomes))*time.Second), outcome))
		}
		return rows
	}
	state := func(s model.Snapshot) model.CheckState {
		t.Helper()
		for _, st := range s.Checks {
			if st.ID == x.ID {
				return st
			}
		}
		t.Fatalf("check %s missing from snapshot", x.ID)
		return model.CheckState{}
	}

	// One violating sample is a warning, not an incident: a publication the
	// runtime has not loaded yet looks exactly like this for a minute.
	s := Build(c, "a", "", samples("policy"), nil, now)
	st := state(s)
	if st.Health != "degraded" || st.PolicyConfirmed || !strings.Contains(st.Reason, "(1/3)") {
		t.Fatalf("a single policy sample was confirmed: %+v", st)
	}
	if len(s.Incidents) != 0 {
		t.Fatalf("incident opened before the confirmation threshold: %+v", s.Incidents)
	}

	// Passing samples break the run, however long the earlier violation was.
	s = Build(c, "a", "", samples("policy", "policy", "ok", "policy"), nil, now)
	if st = state(s); st.PolicyConfirmed || len(s.Incidents) != 0 {
		t.Fatalf("non-consecutive policy samples were confirmed: %+v %+v", st, s.Incidents)
	}

	// A sustained violation is an incident, and still not an outage.
	s = Build(c, "a", "", samples("policy", "policy", "policy"), nil, now)
	st = state(s)
	if st.Health != "degraded" || !st.PolicyConfirmed {
		t.Fatalf("threshold reached but not confirmed: %+v", st)
	}
	if len(s.Incidents) != 1 || !strings.HasSuffix(s.Incidents[0].Scope, "/policy") || !slices.Contains(s.Incidents[0].Checks, x.ID) {
		t.Fatalf("no policy incident after the threshold: %+v", s.Incidents)
	}

	// A stale measurement is history, not a current confirmation. The
	// public field must not claim a policy is confirmed while the same
	// state says its evidence expired.
	s = Build(c, "a", "", samples("policy", "policy", "policy"), nil, now.Add(10*time.Minute))
	st = state(s)
	if st.Freshness != "stale" || st.PolicyConfirmed {
		t.Fatalf("stale policy sample kept its confirmation: %+v", st)
	}
}

func TestProviderSettleExtendsPolicyConfirmation(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	x.IntervalSeconds = 60
	x.FailureThreshold = 3
	settle := 600 // ten samples at the check's interval
	x.Adapter.ProviderReference = &model.MihomoProviderReference{URL: "https://publisher.invalid/members.json", Providers: []string{"edge-local"}, MaxAgeSeconds: 3600, SettleSeconds: &settle}
	c.Checks[0] = x
	now := time.Now().UTC()
	samples := func(n int, settling bool) []model.Observation {
		rows := make([]model.Observation, 0, n)
		for i := 0; i < n; i++ {
			row := testkit.Observation(c, x.ID, uint64(i+1), now.Add(time.Duration(i-n)*time.Second), "policy")
			if settling {
				row.Facts["provider_reference_settling"] = "true"
			}
			rows = append(rows, row)
		}
		return rows
	}
	state := func(s model.Snapshot) model.CheckState {
		t.Helper()
		for _, st := range s.Checks {
			if st.ID == x.ID {
				return st
			}
		}
		t.Fatalf("check %s missing from snapshot", x.ID)
		return model.CheckState{}
	}

	// The settle window is expressed as samples, so a publisher that keeps
	// republishing cannot postpone confirmation past this point.
	s := Build(c, "a", "", samples(9, true), nil, now)
	if st := state(s); st.PolicyConfirmed || len(s.Incidents) != 0 {
		t.Fatalf("confirmed before the settle window: %+v %+v", st, s.Incidents)
	}
	s = Build(c, "a", "", samples(10, true), nil, now)
	st := state(s)
	if !st.PolicyConfirmed || len(s.Incidents) != 1 {
		t.Fatalf("not confirmed after the settle window: %+v %+v", st, s.Incidents)
	}

	// Once the controller is no longer loading the publication, the ordinary
	// failure threshold applies again; the settle samples do not delay a
	// known drift.
	s = Build(c, "a", "", samples(3, false), nil, now)
	if st = state(s); !st.PolicyConfirmed || len(s.Incidents) != 1 {
		t.Fatalf("settle delayed a known drift: %+v %+v", st, s.Incidents)
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
// answer, even an error page, proves the edge and nothing behind it,
// whatever it says of where the answer came from; the origin counts only a
// check of its own.
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
	if !strings.Contains(s.Incidents[0].Hypothesis, "platform's own account") {
		t.Fatal("521 presented as an independent observation of the origin", s.Incidents[0].Hypothesis)
	}

	// An error status is a word the edge or the origin can send whatever
	// happened: the failure layer stays unlocated unless the status class
	// supports a candidate cause, and the origin is never declared at fault
	// from the response alone.
	for _, tt := range []struct{ status, want string }{
		{"403", "edge policy or a challenge page"},
		{"429", "edge policy or a challenge page"},
		{"503", "edge itself or from the origin"},
		{"200", "failing layer is not located"},
	} {
		var statusRows []model.Observation
		for i := 0; i < 3; i++ {
			statusRows = append(statusRows, through(c, "home-http", uint64(i+1), now.Add(time.Duration(i-3)*time.Second), "fail", tt.status, "", true))
		}
		st := Build(c, "a", "home", statusRows, nil, now)
		if len(st.Incidents) != 1 {
			t.Fatalf("HTTP %s: no incident: %+v", tt.status, st.Incidents)
		}
		got := st.Incidents[0].Hypothesis
		if !strings.Contains(got, tt.want) {
			t.Fatalf("HTTP %s: hypothesis %q does not mention %q", tt.status, got, tt.want)
		}
		if strings.Contains(got, "behind it") {
			t.Fatalf("HTTP %s: claimed the fault lies behind the edge: %q", tt.status, got)
		}
	}

	// One incident can aggregate several checks on the same node. It must
	// not present one check's status as the explanation for another's.
	mixedConfig := edged()
	for i := range mixedConfig.Endpoints {
		if mixedConfig.Endpoints[i].ID == "dmit-ssh" {
			mixedConfig.Endpoints[i].Via = []string{"edge"}
		}
	}
	mixed := []model.Observation{}
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i-3) * time.Second)
		mixed = append(mixed, through(mixedConfig, "home-http", uint64(i+1), at, "fail", "521", "", true))
		mixed = append(mixed, through(mixedConfig, "home-ssh", uint64(i+10), at, "fail", "403", "", true))
	}
	st := Build(mixedConfig, "a", "home", mixed, nil, now)
	if len(st.Incidents) != 1 || len(st.Incidents[0].Checks) != 2 {
		t.Fatalf("mixed-status checks did not aggregate: %+v", st.Incidents)
	}
	got := st.Incidents[0].Hypothesis
	if strings.Contains(got, "with HTTP 403") || strings.Contains(got, "with HTTP 521") {
		t.Fatalf("one check's status spoke for the other: %q", got)
	}
	if !strings.Contains(got, "different HTTP statuses") || !strings.Contains(got, "origin's own state is not established") {
		t.Fatalf("mixed statuses were not described honestly: %q", got)
	}

	// Where the edge says its answer came from is its own word, which the
	// edge, or the origin behind it, can send whatever happened: no cache
	// status proves the origin, not even one that says the edge fetched it.
	for _, cache := range []string{"DYNAMIC", "MISS", "EXPIRED", "REVALIDATED", "REFRESHHIT", "PASS", "BYPASS", "", "HIT", "STALE", "UPDATING", "PRERENDER", "LAMBDAGENERATEDRESPONSE", "ERROR"} {
		v := through(c, "cloud-http", 1, now.Add(-time.Second), "ok", "200", cache, true)
		s := Build(c, "a", "cloud", []model.Observation{v}, nil, now)
		if e := findEntity(t, s.Nodes, "edge"); e.Health != "up" {
			t.Fatal("edge answer not counted", cache, e)
		}
		if o := findEntity(t, s.Nodes, "dmit"); o.Health != "unknown" || o.Evidence != "unknown" || !strings.Contains(o.Reason, "proves that hop alone") {
			t.Fatalf("cache %q: the edge's word proved the origin: %+v", cache, o)
		}
	}

	// Nor does it make another observer's answer through the edge
	// evidence against a failure of the origin's own check.
	rows = append(seedFailures(c, "home-ssh", 1, now), through(c, "cloud-http", 1, now.Add(-time.Second), "ok", "200", "MISS", true))
	s = Build(c, "a", "", rows, nil, now)
	for _, inc := range s.Incidents {
		if slices.Contains(inc.Checks, "home-ssh") && slices.Contains(inc.CounterEvidence, "cloud-http") {
			t.Fatal("the edge's word stood against the origin's failure", inc)
		}
	}

	// A check of the origin's own, not through the edge, proves it.
	direct := testkit.Observation(c, "home-ssh", 1, now.Add(-time.Second), "ok")
	direct.Connectivity = true
	direct.Facts = map[string]string{"response_proof": "ssh_banner"}
	s = Build(c, "a", "", []model.Observation{through(c, "cloud-http", 1, now.Add(-time.Second), "ok", "200", "", true), direct}, nil, now)
	if o := findEntity(t, s.Nodes, "dmit"); o.Health != "up" || !strings.Contains(o.Reason, "home-ssh") || strings.Contains(o.Reason, "cloud-http") {
		t.Fatal("the origin's own answer was not what proved it", o)
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

// An observer says whether its executor measures what the collector
// counts: a schema 2 executor by the digest of its contracts, a schema 1
// one by its revision; one that never said is neither. Endpoints carry
// the state of the checks that target them.
func TestObserversSayWhetherTheyMeasureTheCurrentTopology(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	edited := testkit.Config()
	edited.Checks[2].HTTP.Status = []int{204} // cloud-http
	renamed := testkit.Config()
	renamed.Nodes[0].Name = "renamed" // another revision, the same contracts
	beat := func(observer string, h model.Heartbeat) model.Heartbeat {
		h.Project, h.Observer, h.Boot, h.SampledAt = c.Project, observer, "boot", now
		return h
	}
	for _, tc := range []struct {
		name string
		beat model.Heartbeat
		want string
	}{
		{"current", beat("home", model.Heartbeat{Contracts: config.ObserverContracts(renamed, "home")}), "current"},
		{"outdated", beat("cloud", model.Heartbeat{Contracts: config.ObserverContracts(edited, "cloud")}), "outdated"},
		{"legacy_current", beat("home", model.Heartbeat{Revision: config.Revision(c)}), "current"},
		{"legacy_outdated", beat("home", model.Heartbeat{Revision: config.Revision(renamed)}), "outdated"},
		{"silent", model.Heartbeat{}, ""},
	} {
		beats := map[string]model.Heartbeat{}
		if tc.beat.Observer != "" {
			beats[tc.beat.Observer] = tc.beat
		}
		for _, o := range Build(c, "a", "", nil, beats, now).Observers {
			if o.ID == tc.beat.Observer || tc.beat.Observer == "" && o.ID == "home" {
				if o.Contracts != tc.want || o.Health != map[bool]string{true: "unknown", false: "up"}[tc.beat.Observer == ""] {
					t.Fatalf("%s: %+v", tc.name, o)
				}
				if tc.want == "outdated" && !strings.Contains(o.Reason, "refused until it reloads") {
					t.Fatalf("%s: reason %q", tc.name, o.Reason)
				}
			}
		}
	}
	rows := []model.Observation{testkit.Observation(c, "home-ssh", 1, now.Add(-time.Second), "fail")}
	endpoints := map[string]model.EntityState{}
	for _, e := range Build(c, "a", "", rows, nil, now).Endpoints {
		endpoints[e.ID] = e
	}
	if e := endpoints["dmit-ssh"]; e.Health != "degraded" || len(e.Checks) != 1 {
		t.Fatalf("endpoint state %+v", e)
	}
	if e := endpoints["win-tailnet"]; e.Health != "unknown" || len(e.Checks) != 0 || len(endpoints) != len(c.Endpoints) {
		t.Fatalf("unmeasured endpoint %+v", e)
	}
}
