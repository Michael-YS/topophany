package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
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
	details := strings.Join(IncidentDetails(now, model.Incident{Scope: "home/path/p", Since: since}, nil), "\n")
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

// An observer that is alive but measures an outdated topology needs
// attention: its samples of changed checks are refused until it reloads.
func TestOutdatedObserverNeedsAttention(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	edited := testkit.Config()
	edited.Checks[0].HTTP.Status = []int{204}
	beats := map[string]model.Heartbeat{"home": {Project: c.Project, Observer: "home", Boot: "b", SampledAt: now, Contracts: config.ObserverContracts(edited, "home")}}
	v := View{Snapshot: engine.Build(c, "primary", "", nil, beats, now), Now: now}
	var row Row
	for _, r := range v.Rows(Observers) {
		if r.ID == "home" {
			row = r
		}
	}
	if row.Note != "outdated" || !NeedsAttention(row) || State(row) != "outdated" || Pill(row) != "OUTDATED" {
		t.Fatalf("%+v", row)
	}
	if n, _ := v.Badge(Observers); n != 2 { // home outdated, cloud silent
		t.Fatal("badge", n)
	}
	found := false
	for _, r := range v.Rows(Overview) {
		found = found || r.Kind == "observer" && r.ID == "home"
	}
	if !found {
		t.Fatal("the outdated observer is not on the Overview")
	}
	if live, _ := v.Live(); live != 1 {
		t.Fatal("an outdated observer is still live", live)
	}
}

// The Incidents tab lists the incidents open, then those the collector's
// ledger remembers: recovering ones, which still need attention, and those
// closed, each occurrence with an ID of its own and how it ended. An open
// incident's evidence says when it opened and how often it happened
// before, and starts as early as the ledger saw it fail.
func TestIncidentsTabRemembersWhatTheLedgerHolds(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	rows := []model.Observation{}
	for i := uint64(1); i <= 3; i++ {
		rows = append(rows, testkit.Observation(c, "home-http", i, now.Add(-time.Duration(4-i)*time.Second), "fail"))
	}
	snap := engine.Build(c, "primary", "", rows, nil, now)
	open := snap.Incidents[0]
	early := now.Add(-2 * time.Hour)
	snap.Ledger = []model.IncidentRecord{
		{ID: open.ID, Scope: open.Scope, Summary: open.Summary, Checks: open.Checks, State: "open", Since: early, Opened: early.Add(time.Minute), Previous: 2},
		{ID: "aaaaaaaaaaaa/cloud/path/contrast", Scope: "cloud/path/contrast", Summary: "confirmed failure", Checks: []string{"cloud-http"}, State: "recovering", Since: now.Add(-time.Hour), Opened: now.Add(-time.Hour)},
		{ID: open.ID, Scope: open.Scope, Summary: open.Summary, Checks: open.Checks, State: "closed", Since: now.Add(-5 * time.Hour), Opened: now.Add(-5 * time.Hour), Closed: now.Add(-4 * time.Hour), Ended: "recovered", Previous: 1},
		{ID: "bbbbbbbbbbbb/home/home-ssh", Scope: "home/home-ssh", Summary: "confirmed failure", Checks: []string{"home-ssh"}, State: "closed", Since: now.Add(-3 * time.Hour), Opened: now.Add(-3 * time.Hour), Closed: now.Add(-150 * time.Minute), Ended: "unconfirmed"},
	}
	v := View{Snapshot: snap, Now: now}
	got := v.List(Incidents, "")
	if len(got) != 4 {
		t.Fatalf("%d incidents listed", len(got))
	}
	want := []struct{ id, state, pill string }{{open.ID, "DOWN", "OPEN"}, {"aaaaaaaaaaaa/cloud/path/contrast", "recovering", "RECOVERING"}, {open.ID + "#2", "closed", "CLOSED"}, {"bbbbbbbbbbbb/home/home-ssh#1", "closed", "CLOSED"}}
	for i, w := range want {
		if got[i].ID != w.id || State(got[i]) != w.state || Pill(got[i]) != w.pill {
			t.Fatalf("row %d: %s %s %s, want %+v", i, got[i].ID, State(got[i]), Pill(got[i]), w)
		}
	}
	details := strings.Join(got[0].Details, "\n")
	if !strings.Contains(details, "Since: "+Stamp(now, early)) || !strings.Contains(details, "Opened: "+Stamp(now, early.Add(time.Minute))) || !strings.Contains(details, "Seen before: 2 times") || got[0].Cols[0] != "2h 0m ago" {
		t.Fatalf("open incident's evidence:\n%s", details)
	}
	closed := strings.Join(got[3].Details, "\n")
	if got[3].Health != "unknown" || got[2].Health != "up" || !strings.Contains(closed, "Lasted: 30m") || !strings.Contains(closed, "Ended: unconfirmed — its evidence lapsed") || got[3].Cols[0] != "ended 2h 30m ago" {
		t.Fatalf("closed incident:\n%s", closed)
	}
	attention := v.List(Overview, "")
	ids := []string{}
	for _, r := range attention {
		if r.Kind == "incident" {
			ids = append(ids, r.ID)
		}
	}
	if strings.Join(ids, " ") != open.ID+" aaaaaaaaaaaa/cloud/path/contrast" {
		t.Fatalf("the Overview's incidents: %v", ids)
	}
	if n, down := v.Badge(Incidents); n != 2 || !down {
		t.Fatalf("Incidents counts %d", n)
	}
	if v.Verdict().Text != "1 INCIDENT" {
		t.Fatalf("verdict %s", v.Verdict().Text)
	}
	v.Cached = true
	if Pill(v.List(Incidents, "")[2]) != "CACHED" {
		t.Fatal("a cached view claims an incident's state")
	}
}
