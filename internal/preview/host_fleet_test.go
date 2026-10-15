package preview

import (
	"maps"
	"testing"
	"time"
)

func TestBuiltinPreviewHasResourcesForEveryMachine(t *testing.T) {
	for _, lang := range Languages() {
		c, err := Preset(lang)
		if err != nil {
			t.Fatal(err)
		}
		observers := map[string]string{}
		for _, o := range c.Observers {
			observers[o.ID] = o.Node
		}
		hosts := map[string]bool{}
		for _, x := range c.Checks {
			if x.Kind == "host" {
				hosts[observers[x.Observer]] = true
				if x.Host == nil || x.Host.MinProcesses != 1 {
					t.Fatal(x)
				}
			}
		}
		if len(hosts) != 11 {
			t.Fatal(lang, hosts)
		}
		for _, n := range c.Nodes {
			if hosts[n.ID] != (n.Kind == "host" || n.Kind == "router") {
				t.Fatal("resource coverage", lang, n)
			}
		}
		s := New(c, testNow, lang)
		snap, err := s.Snapshot("", testNow)
		if err != nil {
			t.Fatal(err)
		}
		cpu := map[string]bool{}
		for _, st := range snap.Checks {
			if st.Last != nil && st.Last.Facts["simulation"] == "host_resources" {
				if st.Health != "up" {
					t.Fatal(st)
				}
				cpu[st.Last.Facts["cpu_used_percent"]] = true
			}
		}
		if len(cpu) < 2 {
			t.Fatal("all hosts share one sample", cpu)
		}
		mac, err := s.Snapshot("obs-mac", testNow)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, st := range mac.Checks {
			if st.Last != nil && st.Last.Facts["simulation"] == "host_resources" {
				count++
				if st.ID != "host-mac" {
					t.Fatal("cross observer resources", st)
				}
			}
		}
		if count != 1 {
			t.Fatal("Mac resources missing", count)
		}
	}
}

func TestBuiltinResourceScenariosAffectOneNodeAndStopWithItsWorker(t *testing.T) {
	c, err := Preset(DefaultLanguage)
	if err != nil {
		t.Fatal(err)
	}
	for scenario, checkID := range map[string]string{"host-cpu-high": "host-app", "host-memory-high": "host-relay", "host-disk-full": "host-exit-a", "host-process-missing": "host-transit"} {
		s := New(c, testNow, DefaultLanguage)
		hostScenario(t, s, scenario, testNow.Add(-5*time.Minute))
		snap, err := s.Snapshot("", testNow)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range snap.Checks {
			if st.Last == nil || st.Last.Facts["simulation"] != "host_resources" {
				continue
			}
			want := "up"
			if st.ID == checkID {
				want = "degraded"
			}
			if st.Health != want {
				t.Fatal(scenario, st)
			}
		}
	}
	for scenario, id := range map[string]string{"home-offline": "host-router", "relay-down": "host-relay", "transit-down": "host-transit", "app-origin-down": "host-app"} {
		snap := settled(t, scenario)
		st := check(t, snap, id)
		if st.Health != "unknown" || st.Last != nil && st.Freshness == "fresh" {
			t.Fatal("dead worker still reported resources", scenario, st)
		}
	}
	maintenance := settled(t, "aux-maintenance")
	if st := check(t, maintenance, "host-aux"); st.Freshness != "maintenance" {
		t.Fatal(st)
	}
	// Stable capacities and per-host measurements do not depend on the language.
	baseline := settled(t, "normal")
	for _, lang := range Languages() {
		snap := settledIn(t, "normal", lang)
		for _, st := range snap.Checks {
			if st.Last != nil && st.Last.Facts["simulation"] == "host_resources" && !maps.Equal(st.Last.Facts, check(t, baseline, st.ID).Last.Facts) {
				t.Fatal("translated counters changed", lang, st.ID)
			}
		}
	}
}
