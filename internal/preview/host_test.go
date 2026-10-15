package preview

import (
	"maps"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func hostTopology(t *testing.T) model.Config {
	t.Helper()
	c, err := config.Load("../../examples/host-resources.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func hostScenario(t *testing.T, s *Sim, id string, at time.Time) {
	t.Helper()
	i := slices.Index(s.ScenarioIDs(), id)
	if i < 0 {
		t.Fatal("missing scenario", id, s.ScenarioIDs())
	}
	s.SetScenario(i, at)
}

func TestHostPreviewSyntheticCountersAndPolicies(t *testing.T) {
	for _, lang := range Languages() {
		for _, tc := range []struct{ scenario, violation string }{
			{"normal", ""}, {"host-cpu-high", "cpu"}, {"host-memory-high", "memory"}, {"host-disk-full", "disk"}, {"host-process-missing", "process_count"},
		} {
			t.Run(lang+"/"+tc.scenario, func(t *testing.T) {
				c := hostTopology(t)
				// A nonexistent path proves preview does not stat the actual filesystem.
				c.Checks[0].Host.DiskPath = "/topophany-preview-nonexistent"
				s := New(c, testNow.Add(-time.Hour), lang)
				hostScenario(t, s, tc.scenario, testNow.Add(-10*time.Minute))
				snap, err := s.Snapshot("", testNow)
				if err != nil {
					t.Fatal(err)
				}
				st := check(t, snap, "host-resources")
				want := "up"
				if tc.violation != "" {
					want = "degraded"
				}
				if st.Health != want || st.Last == nil || st.Last.Evidence != "reported" || st.Last.Connectivity || st.Last.Facts["violations"] != tc.violation {
					t.Fatal(st)
				}
				o := st.Last
				if o.Facts["simulation"] != "host_resources" || o.PathEvidence != "local_host_runtime_reported" || o.Facts["cpu_logical_count"] == "" || o.Facts["disk_path"] != c.Checks[0].Host.DiskPath {
					t.Fatal(o)
				}
				for _, key := range []string{"cpu_used_percent", "memory_total_bytes", "memory_available_bytes", "memory_used_percent", "disk_total_bytes", "disk_used_percent", "disk_inodes_free", "process_count", "process_matching_count", "process_matching_rss_bytes"} {
					if _, err := strconv.ParseFloat(o.Facts[key], 64); err != nil {
						t.Fatal(key, o.Facts)
					}
				}
				if code := config.ObservationBounds(*o, 13*time.Second); code != "" {
					t.Fatal(code, o)
				}
				if snap.Nodes[0].Health != "unknown" {
					t.Fatal("local telemetry proved remote availability", snap.Nodes)
				}
				if (len(snap.Incidents) > 0) != (tc.violation != "") {
					t.Fatal("policy incident", snap.Incidents)
				}
				history, err := s.History(st.ID, 3, testNow)
				if err != nil || len(history) != 3 || !reflect.DeepEqual(history[0], *o) {
					t.Fatal("history differs from current sample", history, o, err)
				}
				again, err := New(c, testNow.Add(-time.Hour), lang).Snapshot("", testNow)
				if tc.scenario == "normal" && (err != nil || !maps.Equal(again.Checks[0].Last.Facts, o.Facts)) {
					t.Fatal("simulation depends on environment or read order", err)
				}
			})
		}
	}
}

func TestHostPreviewTransitionRecoveryAndConfiguredThresholds(t *testing.T) {
	c := hostTopology(t)
	s := New(c, testNow, DefaultLanguage)
	hostScenario(t, s, "host-cpu-high", testNow.Add(-5*time.Minute))
	bad, err := s.Snapshot("", testNow)
	if err != nil || len(bad.Incidents) != 1 {
		t.Fatal(bad.Incidents, err)
	}
	hostScenario(t, s, "normal", testNow)
	recovered, err := s.Snapshot("", testNow.Add(5*time.Minute))
	if err != nil || len(recovered.Incidents) != 0 || recovered.Checks[0].Health != "up" {
		t.Fatal(recovered.Checks, recovered.Incidents, err)
	}
	if maps.Equal(bad.Checks[0].Last.Facts, recovered.Checks[0].Last.Facts) {
		t.Fatal("counters did not change")
	}
	// Scenarios provide counters; the topology's assertions decide outcomes.
	c.Checks[0].Host = &model.HostOptions{}
	s = New(c, testNow, DefaultLanguage)
	hostScenario(t, s, "host-cpu-high", testNow.Add(-5*time.Minute))
	collectOnly, err := s.Snapshot("", testNow)
	if err != nil || collectOnly.Checks[0].Health != "up" || len(collectOnly.Incidents) != 0 {
		t.Fatal(collectOnly, err)
	}
	c.Checks[0].Host.MaxCPUPercent = 1
	s = New(c, testNow, DefaultLanguage)
	low, err := s.Snapshot("", testNow)
	if err != nil || low.Checks[0].Last.Outcome != "policy" {
		t.Fatal("configured low threshold ignored", low, err)
	}
}

func TestHostScenariosRequireActiveResourceChecks(t *testing.T) {
	c, err := Preset(DefaultLanguage)
	if err != nil {
		t.Fatal(err)
	}
	c.Checks = slices.DeleteFunc(c.Checks, func(x model.Check) bool { return x.Kind == "host" })
	for _, id := range New(c, testNow, DefaultLanguage).ScenarioIDs() {
		if id == "host-cpu-high" {
			t.Fatal("host scenario offered without host checks")
		}
	}
	c = hostTopology(t)
	c.Checks[0].Disabled = true
	if slices.Contains(New(c, testNow, DefaultLanguage).ScenarioIDs(), "host-cpu-high") {
		t.Fatal("disabled host check enabled scenarios")
	}
	c.Checks[0].Disabled = false
	c.Checks[0].Observer = "mac"
	c.Observers[0].ID = "mac"
	snap, err := New(c, testNow, DefaultLanguage).Snapshot("", testNow)
	if err != nil || snap.Checks[0].Last.Facts["os"] != "darwin" || snap.Checks[0].Last.Facts["cpu_sample_ms"] != "1000" {
		t.Fatal(snap.Checks, err)
	}
}
