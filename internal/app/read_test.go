package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/tui"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// at runs topo with its clock stopped at now.
func at(now time.Time, args ...string) (string, error) {
	var out, stderr bytes.Buffer
	a := &App{In: strings.NewReader(""), Out: &out, Err: &stderr, Now: func() time.Time { return now }}
	e := a.Run(context.Background(), args)
	return out.String(), e
}

func exitCode(e error) int {
	var exit *ExitError
	switch {
	case e == nil:
		return 0
	case errors.As(e, &exit):
		return exit.Code
	}
	return 1
}

var escapes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// The read commands show what the TUI shows. For every preview scenario,
// from all observers and from one, each tab's rows as the CLI prints them
// are the rows the TUI lists, evidence included; the verdict the CLI
// gives is the one on the TUI's Overview, and so are the tab counts.
func TestReadCommandsShowWhatTheTUIShows(t *testing.T) {
	c, e := preview.Preset()
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	scenarios := len(preview.New(c, now).Scenarios())
	for sc := 0; sc < scenarios; sc++ {
		for _, observer := range []string{"", "cs-win"} {
			name := fmt.Sprintf("scenario %d from %q", sc, observer)
			sim := preview.New(c, now)
			if sc != 0 {
				sim.SetScenario(sc, now.Add(-30*time.Minute))
			}
			snap, unreachable := sim.Snapshot(observer, now)
			args := func(rest ...string) []string {
				return append([]string{"preview", "--scenario", fmt.Sprint(sc), "--observer", observer}, rest...)
			}
			out, e := at(now, args("status", "--json")...)
			if unreachable != nil {
				// The TUI keeps its last snapshot and marks it cached; the
				// CLI has none to keep and says no collector answered.
				if exitCode(e) != ExitUnreachable {
					t.Fatalf("%s: unreachable collector exits %d: %v", name, exitCode(e), e)
				}
				continue
			}
			var status struct {
				Verdict   view.Verdict
				Status    string
				Tabs      []tabBadge
				Attention []view.Row
			}
			if err := json.Unmarshal([]byte(out), &status); err != nil {
				t.Fatalf("%s: %v\n%s", name, err, out)
			}
			m := &tui.Model{Snapshot: snap, Now: now, Server: "synthetic", Observer: observer, Width: 211, Height: 43, NoColor: true}
			frame := escapes.ReplaceAllString(strings.Join(m.Render(), "\n"), "")
			if verdict := status.Verdict.Glyph + " " + status.Verdict.Text; !strings.Contains(frame, verdict) {
				t.Fatalf("%s: the CLI says %q, the TUI's Overview does not:\n%s", name, verdict, frame)
			}
			tabBar := strings.Split(frame, "\n")[1]
			for i, b := range status.Tabs {
				if b.Count > 0 && !strings.Contains(tabBar, view.Tabs[i]+" "+fmt.Sprint(b.Count)) {
					t.Fatalf("%s: the CLI counts %d in %s, the TUI's tab bar does not: %q", name, b.Count, view.Tabs[i], tabBar)
				}
			}
			if code, want := exitCode(e), map[string]int{"clear": 0, "attention": ExitAttention, "no-evidence": ExitNoEvidence}[status.Status]; code != want {
				t.Fatalf("%s: status %s exits %d, want %d", name, status.Status, code, want)
			}
			for tab := range view.Tabs {
				m.Tab = tab
				want := m.Rows()
				if tab == view.Overview && !reflect.DeepEqual(status.Attention, want) {
					t.Fatalf("%s: status lists other things needing attention than the TUI", name)
				}
				out, e := at(now, args("show", tabNames[tab], "--json")...)
				if e != nil {
					t.Fatalf("%s: show %s: %v", name, tabNames[tab], e)
				}
				var shown struct{ Rows []view.Row }
				if err := json.Unmarshal([]byte(out), &shown); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(shown.Rows, want) {
					t.Fatalf("%s: show %s differs from the TUI's rows", name, tabNames[tab])
				}
			}
		}
	}
}

// The evidence behind every entry reads the same in text as in the TUI's
// evidence panel, line for line, and a server's entry carries its card.
func TestShowEntryTextCarriesTheEvidence(t *testing.T) {
	c, e := preview.Preset()
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	for _, sc := range []int{1, 10} {
		sim := preview.New(c, now)
		sim.SetScenario(sc, now.Add(-30*time.Minute))
		snap, e := sim.Snapshot("", now)
		if e != nil {
			t.Fatal(e)
		}
		v := view.View{Snapshot: snap, Now: now}
		for tab := view.Topology; tab < len(view.Tabs); tab++ {
			for _, r := range v.List(tab, "") {
				out, e := at(now, "preview", "--scenario", fmt.Sprint(sc), "show", tabNames[tab], r.Kind+":"+r.ID)
				if e != nil {
					t.Fatalf("show %s %s:%s: %v", tabNames[tab], r.Kind, r.ID, e)
				}
				lines := map[string]bool{}
				for _, l := range strings.Split(out, "\n") {
					lines[l] = true
				}
				for _, l := range r.Details {
					if l = strings.TrimRight(clean(l), " "); l != "" && !lines[l] {
						t.Fatalf("show %s %s lacks the evidence line %q:\n%s", tabNames[tab], r.ID, l, out)
					}
				}
				if tab == view.Servers {
					card := v.Fleet().Card(r.ID)
					for _, s := range card.Services {
						if !strings.Contains(out, s.Name) {
							t.Fatalf("server %s's card lacks service %s:\n%s", r.ID, s.Name, out)
						}
					}
					for _, ep := range append(card.Addresses, card.Fronts...) {
						if !strings.Contains(out, ep.Address) {
							t.Fatalf("server %s's card lacks address %s:\n%s", r.ID, ep.Address, out)
						}
					}
				}
			}
		}
	}
}

// The read commands against a real collector: status exits by what it
// finds, show and history read it, flags may follow the arguments, and a
// collector that does not answer is told apart from a refusal.
func TestReadCommandsAgainstACollector(t *testing.T) {
	c := testkit.Config()
	st, e := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Observers: observerIDs(c)})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	viewer := strings.Repeat("v", 64)
	backend := api.New(c, "remote", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(viewer), Role: "viewer"}}})
	// The collector serves a snapshot for a second; its clock moves on
	// past that whenever the test writes evidence.
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	backend.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	srv := httptest.NewServer(backend.Handler())
	defer srv.Close()
	conn := []string{"--credentials", filepath.Join(t.TempDir(), "none.json"), "--server", srv.URL, "--project", c.Project, "--credential", viewer}
	read := func(args ...string) (string, error) { return at(time.Now(), append(args, conn...)...) }

	out, e := read("status")
	if exitCode(e) != ExitNoEvidence || !strings.Contains(out, "○ NO EVIDENCE") || !strings.Contains(out, "Collector: remote @ "+srv.URL) {
		t.Fatalf("nothing measured yet: exit %d, %v\n%s", exitCode(e), e, out)
	}
	now := time.Now().UTC()
	batch := model.Batch{Project: c.Project, Revision: config.Revision(c)}
	for i := 0; i < 3; i++ {
		batch.Observations = append(batch.Observations, testkit.Observation(c, "home-http", uint64(i+1), now.Add(-time.Duration(3-i)*time.Second), "fail"))
	}
	if e = st.Put(batch); e != nil {
		t.Fatal(e)
	}
	clock.Add(int64(2 * time.Second))
	out, e = read("status")
	if exitCode(e) != ExitAttention || !strings.Contains(out, "INCIDENT") || !strings.Contains(out, "Checks: 0 up, 0 warning, 1 down, 5 unknown") {
		t.Fatalf("a confirmed failure: exit %d, %v\n%s", exitCode(e), e, out)
	}
	out, e = read("show", "checks", "home-http", "--json")
	var shown struct {
		Server string
		Rows   []view.Row
	}
	if e != nil || json.Unmarshal([]byte(out), &shown) != nil || len(shown.Rows) != 1 || shown.Rows[0].Health != "down" || shown.Server != srv.URL {
		t.Fatalf("show a check: %v\n%s", e, out)
	}
	out, e = read("history", "home-http", "--limit", "2")
	if e != nil || strings.Count(out, " fail ") != 2 || !strings.Contains(out, "Samples: 2, newest first") {
		t.Fatalf("history: %v\n%s", e, out)
	}
	out, e = read("history", "home-http", "--json")
	var history struct{ Entries []model.Observation }
	if e != nil || json.Unmarshal([]byte(out), &history) != nil || len(history.Entries) != 3 || !history.Entries[0].SampledAt.After(history.Entries[2].SampledAt) {
		t.Fatalf("history as JSON: %v\n%s", e, out)
	}
	if _, e = read("history", "no-such-check"); e == nil || exitCode(e) == ExitUnreachable {
		t.Fatal("an unknown check is a refusal, not an outage:", e)
	}
	if _, e = read("show", "checks", "no-such-check"); exitCode(e) != 1 {
		t.Fatal("an unknown entry:", e)
	}
	for _, args := range [][]string{{"status", "extra"}, {"show"}, {"show", "nowhere"}, {"show", "map", "x"}, {"history"}, {"history", "home-http", "--limit", "0"}} {
		if _, e = read(args...); exitCode(e) != 1 {
			t.Fatal("bad usage accepted:", args, e)
		}
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	for _, args := range [][]string{{"status"}, {"show", "checks"}, {"history", "home-http"}} {
		_, e = at(time.Now(), append(args, "--credentials", filepath.Join(t.TempDir(), "none.json"), "--server", dead.URL, "--project", c.Project, "--credential", viewer)...)
		if exitCode(e) != ExitUnreachable {
			t.Fatal("no collector answered", args, ":", e)
		}
	}
}

// Text output never carries a control sequence from the evidence.
func TestTextOutputDropsControlSequences(t *testing.T) {
	var b bytes.Buffer
	w := &text{w: &b}
	w.kv("Name", "evil\x1b[31mred\x1b]2;title\x07\u202e")
	w.table([][]string{{"A", "B"}, {"x\x1b[2J", "multi\nline"}})
	w.entry(view.Row{Kind: "node", ID: "n", Title: "\x1b[1mbold", Details: []string{"Notes: \u009b31m"}})
	if strings.ContainsAny(b.String(), "\x1b\x07\u009b\u202e") {
		t.Fatalf("%q", b.String())
	}
	if !strings.Contains(b.String(), "multi line") {
		t.Fatalf("a newline in a cell should read as a space: %q", b.String())
	}
}

// topo preview lists its scenarios and refuses commands it does not have.
func TestPreviewScenariosAndCommands(t *testing.T) {
	now := time.Now()
	out, e := at(now, "preview", "--scenario", "2", "scenarios", "--json")
	var list []struct {
		Index   int
		Name    string
		Current bool
	}
	if e != nil || json.Unmarshal([]byte(out), &list) != nil || len(list) < 3 || !list[2].Current || list[0].Current {
		t.Fatalf("%v\n%s", e, out)
	}
	if out, e = at(now, "preview", "--scenario", "2", "scenarios"); e != nil || !strings.Contains(out, "2*") {
		t.Fatalf("%v\n%s", e, out)
	}
	if out, e = at(now, "preview", "--scenario", "1", "status"); !strings.Contains(out, "Scenario: "+list[1].Name) || exitCode(e) != ExitAttention {
		t.Fatalf("%v\n%s", e, out)
	}
	for _, args := range [][]string{{"preview", "nonsense"}, {"preview", "--print-topology", "status"}, {"preview", "status", "--observer", "nobody"}} {
		if _, e = at(now, args...); exitCode(e) != 1 {
			t.Fatal("accepted", args, e)
		}
	}
}

// When every check passes and every observer reports, status says so and
// exits 0.
func TestStatusAllClearExitsZero(t *testing.T) {
	c := DemoConfig()
	st, e := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Observers: observerIDs(c)})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	now := time.Now().UTC()
	beat := &model.Heartbeat{Observer: "demo", Boot: "boot", SampledAt: now}
	if e = st.Put(model.Batch{Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "api-health", 1, now.Add(-time.Second), "ok")}, Heartbeat: beat}); e != nil {
		t.Fatal(e)
	}
	viewer := strings.Repeat("v", 64)
	srv := httptest.NewServer(api.New(c, "remote", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(viewer), Role: "viewer"}}}).Handler())
	defer srv.Close()
	out, e := at(time.Now(), "status", "--credentials", filepath.Join(t.TempDir(), "none.json"), "--server", srv.URL, "--project", c.Project, "--credential", viewer)
	if e != nil || !strings.Contains(out, "● ALL CLEAR") || !strings.Contains(out, "✓ Nothing needs attention") || !strings.Contains(out, "Needs a look: nothing") {
		t.Fatalf("exit %d, %v\n%s", exitCode(e), e, out)
	}
}
