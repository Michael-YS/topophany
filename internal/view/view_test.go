package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Moments in the evidence read the same everywhere: local time to the
// second, and for an incident's start, how long ago that was.
func TestEvidenceTimesAreReadable(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	since := now.Add(-125 * time.Second)
	if got, want := Stamp(now, since), since.Local().Format("2006-01-02 15:04:05 MST")+" · 2m ago"; got != want {
		t.Fatalf("stamp %q, want %q", got, want)
	}
	if Stamp(now, time.Time{}) != "never" {
		t.Fatal("a zero time is not never")
	}
	details := strings.Join(IncidentDetails(now, model.Incident{Scope: "home/path/p", Since: since}), "\n")
	if !strings.Contains(details, "Since: "+Stamp(now, since)) || strings.Contains(details, "T"+since.Local().Format("15:04:05")) {
		t.Fatalf("incident start is not readable:\n%s", details)
	}
}

// An endpoint is judged as the engine judges a service or a node: one
// whose only check has failed once, short of confirmation, is degraded,
// not unknown, on the Topology tab and on its server's card alike.
func TestEndpointHealthFollowsTheEngine(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	snap := engine.Build(c, "primary", "", []model.Observation{testkit.Observation(c, "home-ssh", 1, now.Add(-time.Second), "fail")}, nil, now)
	v := View{Snapshot: snap, Now: now}
	if got := checkHealth(snap, "home-ssh"); got != "degraded" {
		t.Fatalf("fixture check is %s, want degraded", got)
	}
	found := false
	for _, r := range v.Rows(Topology) {
		if r.Kind == "endpoint" && r.ID == "dmit-ssh" {
			found = true
			if r.Health != "degraded" {
				t.Fatalf("endpoint row is %s, want degraded", r.Health)
			}
		}
	}
	if !found {
		t.Fatal("no endpoint row for dmit-ssh")
	}
	for _, ep := range v.Fleet().Card("dmit").Addresses {
		if ep.ID == "dmit-ssh" && ep.Health != "degraded" {
			t.Fatalf("card address is %s, want degraded", ep.Health)
		}
	}
	// A cached view knows nothing current about it.
	v.Cached = true
	for _, ep := range v.Fleet().Card("dmit").Addresses {
		if ep.ID == "dmit-ssh" && ep.Health != "unknown" {
			t.Fatalf("cached card address is %s, want unknown", ep.Health)
		}
	}
}

func checkHealth(s model.Snapshot, id string) string {
	for _, cs := range s.Checks {
		if cs.ID == id {
			return cs.Health
		}
	}
	return ""
}
