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
func TestHeartbeatDoesNotRefreshCheckAndForeignRevisionIgnored(t *testing.T) {
	c := testkit.Config()
	now := time.Now()
	v := testkit.Observation(c, "home-http", 1, now.Add(-time.Hour), "ok")
	beats := map[string]model.Heartbeat{"home": {Observer: "home", Boot: "new", SampledAt: now}}
	s := Build(c, "a", "home", []model.Observation{v}, beats, now)
	if s.Observers[0].Health != "up" || s.Checks[0].Freshness != "stale" {
		t.Fatal("heartbeat confused with fresh check")
	}
	v.Revision = "old-target-revision"
	v.SampledAt = now
	s = Build(c, "a", "home", []model.Observation{v}, beats, now)
	if s.Checks[0].Freshness != "never" {
		t.Fatal("old test URL reused in new measurement contract")
	}
	v.Revision = config.Revision(c)
	v.Observer = "cloud"
	s = Build(c, "a", "home", []model.Observation{v}, nil, now)
	if s.Checks[0].Last != nil {
		t.Fatal("foreign observer result accepted")
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
