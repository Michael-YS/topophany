package ledger

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// history feeds one check's samples to the engine and judges them as a
// collector would at each moment.
type history struct {
	t     *testing.T
	c     model.Config
	l     *Ledger
	rows  []model.Observation
	seq   uint64
	clock time.Time
}

func (h *history) sample(check string, outcomes ...string) {
	for _, o := range outcomes {
		h.seq++
		h.clock = h.clock.Add(time.Minute)
		h.rows = append(h.rows, testkit.Observation(h.c, check, h.seq, h.clock, o))
	}
}

func (h *history) judge(settled bool) []Change {
	h.t.Helper()
	changes, e := h.l.Observe(engine.Build(h.c, "test", "", h.rows, nil, h.clock), h.clock, settled)
	if e != nil {
		h.t.Fatal(e)
	}
	return changes
}

func kinds(changes []Change) string {
	out := []string{}
	for _, c := range changes {
		out = append(out, c.Kind)
	}
	return strings.Join(out, ",")
}

// An incident opens when it is confirmed and closes only once its checks
// pass again as many times as their recovery threshold asks: until then
// it is recovering, and a failure in between is a relapse of the same
// incident, not a new one. A later one of the same scope counts the first.
func TestIncidentsOpenRecoverAndClose(t *testing.T) {
	c := testkit.Config()
	h := &history{t: t, c: c, l: New(), clock: time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)}
	h.sample("home-http", "fail", "fail")
	if got := kinds(h.judge(true)); got != "" {
		t.Fatalf("an unconfirmed failure %s an incident", got)
	}
	h.sample("home-http", "fail")
	changes := h.judge(true)
	if kinds(changes) != "opened" {
		t.Fatalf("a confirmed failure: %s", kinds(changes))
	}
	opened := changes[0].Record
	if opened.State != "open" || !opened.Opened.Equal(h.clock) || !opened.Since.Equal(h.clock.Add(-2*time.Minute)) || opened.Previous != 0 || opened.Checks[0] != "home-http" {
		t.Fatalf("opened %+v", opened)
	}
	if !strings.HasPrefix(changes[0].Line(), "incident opened: home/path/direct (") || !strings.HasSuffix(changes[0].Line(), "); checks: home-http") {
		t.Fatalf("logged as %q", changes[0].Line())
	}
	if got := kinds(h.judge(true)); got != "" {
		t.Fatalf("an incident still open %s again", got)
	}
	h.sample("home-http", "ok")
	if got := kinds(h.judge(true)); got != "recovering" {
		t.Fatalf("one pass short of recovery: %s", got)
	}
	h.sample("home-http", "fail")
	if got := kinds(h.judge(true)); got != "relapsed" {
		t.Fatalf("a failure while recovering: %s", got)
	}
	h.sample("home-http", "ok", "ok")
	changes = h.judge(true)
	if kinds(changes) != "closed" || changes[0].Record.Ended != Recovered || !changes[0].Record.Closed.Equal(h.clock) || changes[0].Record.ID != opened.ID {
		t.Fatalf("a recovery: %s %+v", kinds(changes), changes)
	}
	if !strings.HasSuffix(changes[0].Line(), ", recovered after 6m0s") {
		t.Fatalf("logged as %q", changes[0].Line())
	}
	h.sample("home-http", "fail", "fail", "fail")
	changes = h.judge(true)
	if kinds(changes) != "opened" || changes[0].Record.Previous != 1 || changes[0].Record.ID != opened.ID {
		t.Fatalf("the same incident again: %s %+v", kinds(changes), changes)
	}
	records := h.l.Records()
	if len(records) != 2 || records[0].State != "closed" || records[1].State != "open" {
		t.Fatalf("records %+v", records)
	}
}

// When the evidence of an open incident lapses, a collector that has just
// started keeps it open, since its workers may yet deliver; a settled one
// closes it without claiming a recovery. Checks paused, or no longer
// measuring what they did, close it as such.
func TestIncidentsEndAsTheEvidenceSays(t *testing.T) {
	c := testkit.Config()
	h := &history{t: t, c: c, l: New(), clock: time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)}
	h.sample("home-http", "fail", "fail", "fail")
	h.judge(true)
	h.clock = h.clock.Add(time.Hour)
	if got := kinds(h.judge(false)); got != "" {
		t.Fatalf("a collector just started %s an incident whose evidence lapsed", got)
	}
	changes := h.judge(true)
	if kinds(changes) != "closed" || changes[0].Record.Ended != Unconfirmed {
		t.Fatalf("lapsed evidence: %+v", changes)
	}
	for _, edit := range []struct {
		name, ended string
		change      func(*model.Config)
	}{
		{"paused", Paused, func(c *model.Config) { c.Checks[0].Disabled = true }},
		{"maintenance", Paused, func(c *model.Config) { c.Checks[0].Maintenance = true }},
		{"removed", Changed, func(c *model.Config) { c.Checks = c.Checks[1:] }},
	} {
		c := testkit.Config()
		h := &history{t: t, c: c, l: New(), clock: time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)}
		h.sample("home-http", "fail", "fail", "fail")
		h.judge(true)
		edit.change(&h.c)
		changes := h.judge(false)
		if kinds(changes) != "closed" || changes[0].Record.Ended != edit.ended {
			t.Fatalf("%s: %+v", edit.name, changes)
		}
	}
	// What a check measures changed while it still fails: its incident has
	// a new ID, and the old record closes as changed.
	c = testkit.Config()
	h = &history{t: t, c: c, l: New(), clock: time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)}
	h.sample("home-http", "fail", "fail", "fail")
	h.judge(true)
	snap := engine.Build(c, "test", "", h.rows, nil, h.clock)
	snap.Incidents[0].ID = "0123456789ab/" + snap.Incidents[0].Scope
	changes, _ = h.l.Observe(snap, h.clock, true)
	if kinds(changes) != "opened,closed" || changes[1].Record.Ended != Changed {
		t.Fatalf("a re-identified incident: %s %+v", kinds(changes), changes)
	}
}

// The ledger outlives the collector: it is written whenever it changes and
// read back on start. One it cannot read is refused, not replaced, and
// closed records past the retention or beyond the bound are dropped, open
// ones never.
func TestLedgerSurvivesRestartWithinItsBounds(t *testing.T) {
	c := testkit.Config()
	path := filepath.Join(t.TempDir(), "incidents.json")
	l, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	h := &history{t: t, c: c, l: l, clock: time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)}
	h.sample("home-http", "fail", "fail", "fail")
	h.judge(true)
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal("ledger not written", e)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("ledger not written privately", info.Mode())
	}
	again, e := Open(path)
	if e != nil || len(again.Records()) != 1 || again.Records()[0].State != "open" {
		t.Fatalf("ledger not read back: %v %+v", e, again.Records())
	}
	for _, bad := range []string{"{", `{"schema":2,"records":[]}`, `{"schema":1,"records":[{"id":"x","scope":"s","summary":"","checks":[],"state":"lost","since":"2026-10-13T18:00:00Z","opened":"2026-10-13T18:00:00Z","previous":0}]}`} {
		if e := os.WriteFile(path, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Open(path); e == nil || !strings.Contains(e.Error(), "move it aside") {
			t.Fatalf("unreadable ledger %q accepted: %v", bad, e)
		}
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	records := []model.IncidentRecord{{ID: "a/home/x", Scope: "home/x", State: "open", Since: now, Opened: now}}
	for i := range MaxRecords + 5 {
		at := now.Add(time.Duration(i) * time.Second)
		records = append(records, model.IncidentRecord{ID: "b/home/y", Scope: "home/y", State: "closed", Since: at, Opened: at, Closed: at, Ended: Recovered, Previous: i})
	}
	records = append(records, model.IncidentRecord{ID: "c/home/z", Scope: "home/z", State: "closed", Since: now.Add(-Retention - 2*time.Hour), Opened: now.Add(-Retention - 2*time.Hour), Closed: now.Add(-Retention - time.Hour), Ended: Recovered})
	l = With(records)
	l.prune(now.Add(time.Hour))
	kept := l.Records()
	if len(kept) != MaxRecords || kept[0].ID != "a/home/x" || kept[1].Previous != 6 {
		t.Fatalf("pruned to %d records, first %s, next previous %d", len(kept), kept[0].ID, kept[1].Previous)
	}
}

// A snapshot carries the records open and those closed in the last day,
// at most MaxRecent of them, newest first; a view of one observer only
// the records of its scopes.
func TestRecentIsWhatASnapshotCarries(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	records := []model.IncidentRecord{
		{ID: "a/home/x", Scope: "home/x", State: "closed", Since: now, Opened: now.Add(-48 * time.Hour), Closed: now.Add(-47 * time.Hour), Ended: Recovered},
		{ID: "b/cloud/y", Scope: "cloud/y", State: "recovering", Since: now, Opened: now},
		{ID: "c/home/z", Scope: "home/z", State: "open", Since: now, Opened: now},
	}
	for i := range MaxRecent + 3 {
		at := now.Add(-time.Duration(MaxRecent+3-i) * time.Minute)
		records = append(records, model.IncidentRecord{ID: "d/home/w", Scope: "home/w", State: "closed", Since: at, Opened: at, Closed: at, Ended: Unconfirmed, Previous: i})
	}
	l := With(records)
	all := l.Recent("", now)
	if len(all) != 2+MaxRecent || all[0].ID != "c/home/z" || all[1].ID != "b/cloud/y" || all[2].Previous != MaxRecent+2 {
		t.Fatalf("recent: %d records, %s, %s", len(all), all[0].ID, all[1].ID)
	}
	home := l.Recent("home", now)
	for _, r := range home {
		if !strings.HasPrefix(r.Scope, "home/") {
			t.Fatalf("view of home carries %s", r.Scope)
		}
	}
	if len(home) != 1+MaxRecent {
		t.Fatalf("view of home carries %d records", len(home))
	}
	home[0].Checks = append(home[0].Checks, "mutated")
	if len(l.Recent("home", now)[0].Checks) != 0 {
		t.Fatal("a reader can change the ledger")
	}
}

func TestSettleWaitsForTheStalestCheck(t *testing.T) {
	c := testkit.Config()
	if got := Settle(c); got != 180*time.Second {
		t.Fatalf("settle %s", got)
	}
	c.Checks[0].StaleSeconds = 3600
	if got := Settle(c); got != 15*time.Minute {
		t.Fatalf("settle %s", got)
	}
	if got := Settle(model.Config{}); got != 10*time.Second {
		t.Fatalf("settle %s", got)
	}
}
