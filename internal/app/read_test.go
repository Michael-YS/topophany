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
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
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
	c, e := preview.Preset(preview.DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	scenarios := len(preview.New(c, now, preview.DefaultLanguage).Scenarios())
	for sc := 0; sc < scenarios; sc++ {
		for _, observer := range []string{"", "obs-win"} {
			name := fmt.Sprintf("scenario %d from %q", sc, observer)
			sim := preview.New(c, now, preview.DefaultLanguage)
			if sc != 0 {
				sim.SetScenario(sc, now.Add(-30*time.Minute))
			}
			snap, unreachable := sim.Snapshot(observer, now)
			args := func(rest ...string) []string {
				return append([]string{"preview", "--lang", preview.DefaultLanguage, "--scenario", fmt.Sprint(sc), "--observer", observer}, rest...)
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
			// The CLI lists every row; the TUI may fold Services and Checks
			// into groups by application, which is presentation only.
			m := &tui.Model{Snapshot: snap, Now: now, Server: "synthetic", Observer: observer, Width: 211, Height: 43, NoColor: true,
				GroupBy: map[int]string{view.Services: "flat", view.Checks: "flat"}}
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
	c, e := preview.Preset(preview.DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	for _, sc := range []int{1, 10} {
		sim := preview.New(c, now, preview.DefaultLanguage)
		sim.SetScenario(sc, now.Add(-30*time.Minute))
		snap, e := sim.Snapshot("", now)
		if e != nil {
			t.Fatal(e)
		}
		v := view.View{Snapshot: snap, Now: now}
		for tab := view.Topology; tab < len(view.Tabs); tab++ {
			for _, r := range v.List(tab, "") {
				out, e := at(now, "preview", "--lang", preview.DefaultLanguage, "--scenario", fmt.Sprint(sc), "show", tabNames[tab], r.Kind+":"+r.ID)
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

// A check shown on its own carries its recent samples as its evidence
// panel has them: the strip topo show prints is the newest part of the one
// the TUI draws, from the same samples, and its text says the same.
func TestShowCheckCarriesItsRecentSamples(t *testing.T) {
	c, e := preview.Preset(preview.DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	const id = "w-transit-443"
	// All clear, a lossy border, then a worker gone quiet.
	for _, sc := range []int{0, 5, 6} {
		sim := preview.New(c, now, preview.DefaultLanguage)
		if sc != 0 {
			sim.SetScenario(sc, now.Add(-30*time.Minute))
		}
		snap, e := sim.Snapshot("", now)
		if e != nil {
			t.Fatal(e)
		}
		entries, e := sim.History(id, view.StripSamples, now)
		if e != nil {
			t.Fatal(e)
		}
		x, _ := config.Check(c, id)
		want := view.NewStrip(x, entries, now, textSlots)
		out, e := at(now, "preview", "--lang", preview.DefaultLanguage, "--scenario", fmt.Sprint(sc), "show", "checks", id, "--json")
		if e != nil {
			t.Fatal(e)
		}
		var shown struct{ Rows []shown }
		if err := json.Unmarshal([]byte(out), &shown); err != nil || len(shown.Rows) != 1 || shown.Rows[0].Samples == nil {
			t.Fatalf("scenario %d: show carries no samples: %v\n%s", sc, err, out)
		}
		if got := *shown.Rows[0].Samples; !reflect.DeepEqual(roundTrip(t, got), roundTrip(t, want)) || got.Samples == 0 && sc != 6 {
			t.Fatalf("scenario %d: show's strip %q, want %q", sc, view.Glyphs(got.Slots), view.Glyphs(want.Slots))
		}
		m := &tui.Model{Snapshot: snap, Now: now, Server: "synthetic", Width: 211, Height: 43, NoColor: true, Tab: view.Checks, Detail: true}
		for i, r := range m.Rows() {
			if r.ID == id {
				m.Selected = i
			}
		}
		m.KeepSamples(id, entries, nil)
		frame := escapes.ReplaceAllString(strings.Join(m.Render(), "\n"), "")
		if !strings.Contains(frame, view.Glyphs(want.Slots)) {
			t.Fatalf("scenario %d: the TUI's strip does not end as show's %q:\n%s", sc, view.Glyphs(want.Slots), frame)
		}
		text, e := at(now, "preview", "--lang", preview.DefaultLanguage, "--scenario", fmt.Sprint(sc), "show", "checks", id)
		if e != nil || !strings.Contains(text, "RECENT SAMPLES · "+want.Span(now)+" · "+want.Every()+"\n"+view.Glyphs(want.Slots)+"\n") || !strings.Contains(text, want.Latency.String()) {
			t.Fatalf("scenario %d: show's text lacks the strip: %v\n%s", sc, e, text)
		}
	}
}

func roundTrip(t *testing.T, s view.Strip) any {
	t.Helper()
	b, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	var out any
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
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
	conn := []string{"--credentials", filepath.Join(t.TempDir(), "none.json"), "--server", srv.URL, "--project", c.Project, "--credential-file", credentialFile(t, viewer)}
	read := func(args ...string) (string, error) { return at(time.Now(), append(args, conn...)...) }

	out, e := read("status")
	if exitCode(e) != ExitNoEvidence || !strings.Contains(out, "○ NO EVIDENCE") || !strings.Contains(out, "Collector: remote @ "+srv.URL) {
		t.Fatalf("nothing measured yet: exit %d, %v\n%s", exitCode(e), e, out)
	}
	now := time.Now().UTC()
	batch := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c)}
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
		Rows   []shown
	}
	if e != nil || json.Unmarshal([]byte(out), &shown) != nil || len(shown.Rows) != 1 || shown.Rows[0].Health != "down" || shown.Server != srv.URL {
		t.Fatalf("show a check: %v\n%s", e, out)
	}
	if s := shown.Rows[0].Samples; s == nil || s.Samples != 3 || view.Glyphs(s.Slots) != "✖✖✖" {
		t.Fatalf("show a check without its recent samples:\n%s", out)
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
	out, e = read("history", "home-http", "--step", "5m", "--span", "1h")
	if e != nil || !strings.Contains(out, "Rollups: 5m slots from ") || !strings.Contains(out, "Total: 0.0% up over <1% measured · down ") || !strings.Contains(out, "FROM  ") {
		t.Fatalf("history by five minutes: %v\n%s", e, out)
	}
	out, e = read("history", "home-http", "--span", "2h", "--json")
	var rolled struct {
		client.Rollups
		Server string
	}
	if e != nil || json.Unmarshal([]byte(out), &rolled) != nil || rolled.Step != 3600 || rolled.Total.Samples != 3 || rolled.Total.Outcomes["fail"] != 3 || len(rolled.Slots) != 3 || rolled.Server != srv.URL {
		t.Fatalf("history by the hour as JSON: %v\n%s", e, out)
	}
	if _, e = read("show", "checks", "no-such-check"); exitCode(e) != 1 {
		t.Fatal("an unknown entry:", e)
	}
	for _, args := range [][]string{{"status", "extra"}, {"show"}, {"show", "nowhere"}, {"show", "map", "x"}, {"history"}, {"history", "home-http", "--limit", "0"}, {"history", "home-http", "--step", "10m"}, {"history", "home-http", "--step", "5m", "--span", "30d"}, {"history", "home-http", "--span", "a week"}} {
		if _, e = read(args...); exitCode(e) != 1 {
			t.Fatal("bad usage accepted:", args, e)
		}
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	for _, args := range [][]string{{"status"}, {"show", "checks"}, {"history", "home-http"}, {"history", "home-http", "--step", "1d"}} {
		_, e = at(time.Now(), append(args, "--credentials", filepath.Join(t.TempDir(), "none.json"), "--server", dead.URL, "--project", c.Project, "--credential-file", credentialFile(t, viewer))...)
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
	w.entry(view.Row{Kind: "node", ID: "n", Title: "\x1b[1mbold", Details: []string{"Notes: \u009b31m"}}, func() {})
	w.samples(nil, "refused\x1b[2J", time.Now())
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
	out, e := at(now, "preview", "--lang", preview.DefaultLanguage, "--scenario", "2", "scenarios", "--json")
	var list []struct {
		Index   int
		ID      string
		Name    string
		Current bool
	}
	if e != nil || json.Unmarshal([]byte(out), &list) != nil || len(list) < 3 || !list[2].Current || list[0].Current || list[1].ID != "transit-blocked" {
		t.Fatalf("%v\n%s", e, out)
	}
	if out, e = at(now, "preview", "--lang", preview.DefaultLanguage, "--scenario", "2", "scenarios"); e != nil || !strings.Contains(out, "2*") {
		t.Fatalf("%v\n%s", e, out)
	}
	if out, e = at(now, "preview", "--lang", preview.DefaultLanguage, "--scenario", "1", "status"); !strings.Contains(out, "Scenario: "+list[1].Name) || exitCode(e) != ExitAttention {
		t.Fatalf("%v\n%s", e, out)
	}
	for _, args := range [][]string{{"preview", "nonsense"}, {"preview", "--print-topology", "status"}, {"preview", "status", "--observer", "nobody"}, {"preview", "--lang", "tlh", "status"}} {
		if _, e = at(now, args...); exitCode(e) != 1 {
			t.Fatal("accepted", args, e)
		}
	}
}

// topo preview speaks every language it has a catalog for, chosen by
// --lang or else by the locale, and the scenarios keep their IDs in each.
func TestPreviewLanguages(t *testing.T) {
	now := time.Now()
	base, e := at(now, "preview", "--lang", preview.DefaultLanguage, "scenarios", "--json")
	if e != nil {
		t.Fatal(e)
	}
	for _, lang := range preview.Languages() {
		out, e := at(now, "preview", "--lang", lang, "scenarios", "--json")
		var list, want []struct{ ID, Name string }
		if e != nil || json.Unmarshal([]byte(out), &list) != nil || json.Unmarshal([]byte(base), &want) != nil || len(list) != len(want) {
			t.Fatalf("%s: %v\n%s", lang, e, out)
		}
		for i := range list {
			if list[i].ID != want[i].ID || (lang != preview.DefaultLanguage) == (list[i].Name == want[i].Name) {
				t.Fatalf("%s: scenario %d is %+v, %+v in %s", lang, i, list[i], want[i], preview.DefaultLanguage)
			}
		}
		topo, e := at(now, "preview", "--lang", lang, "--print-topology")
		want0, _ := preview.PresetJSON(lang)
		if e != nil || topo != string(want0) {
			t.Fatalf("%s: --print-topology wrote another topology: %v", lang, e)
		}
	}
	for _, env := range []struct{ all, messages, lang, want string }{
		{"", "", "", preview.DefaultLanguage},
		{"", "", "ja_JP.UTF-8", "ja"},
		{"", "zh_CN.UTF-8", "ja_JP.UTF-8", "zh"},
		{"C", "zh_CN.UTF-8", "ja_JP.UTF-8", preview.DefaultLanguage},
		{"", "", "tlh", preview.DefaultLanguage},
	} {
		t.Setenv("LC_ALL", env.all)
		t.Setenv("LC_MESSAGES", env.messages)
		t.Setenv("LANG", env.lang)
		if got := localeLanguage(); got != env.want {
			t.Errorf("LC_ALL=%q LC_MESSAGES=%q LANG=%q chose %q, not %q", env.all, env.messages, env.lang, got, env.want)
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
	if e = st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "api-health", 1, now.Add(-time.Second), "ok")}, Heartbeat: beat}); e != nil {
		t.Fatal(e)
	}
	viewer := strings.Repeat("v", 64)
	srv := httptest.NewServer(api.New(c, "remote", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(viewer), Role: "viewer"}}}).Handler())
	defer srv.Close()
	out, e := at(time.Now(), "status", "--credentials", filepath.Join(t.TempDir(), "none.json"), "--server", srv.URL, "--project", c.Project, "--credential-file", credentialFile(t, viewer))
	if e != nil || !strings.Contains(out, "● ALL CLEAR") || !strings.Contains(out, "✓ Nothing needs attention") || !strings.Contains(out, "Needs a look: nothing") {
		t.Fatalf("exit %d, %v\n%s", exitCode(e), e, out)
	}
}

// How a check fared reads the same in its evidence, on the Checks tab and
// from its rollups: an outage of half an hour adds half an hour down to
// the last day, and a worker gone quiet leaves its checks unmeasured
// rather than up.
func TestHowChecksFaredFromRollups(t *testing.T) {
	c, e := preview.Preset(preview.DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	ids := preview.New(c, now, preview.DefaultLanguage).ScenarioIDs()
	index := func(id string) int { return slices.Index(ids, id) }
	const id = "w-transit-443"
	// The normal scenario fails a sample in a hundred or so, and so do the
	// others besides what they break.
	calm, e := preview.New(c, now, preview.DefaultLanguage).Snapshot("", now)
	if e != nil {
		t.Fatal(e)
	}
	var usual model.Availability
	for _, cs := range calm.Checks {
		if cs.ID == id && len(cs.Availability) == 3 {
			usual = cs.Availability[0]
		}
	}
	if usual.Unmeasured != 0 || usual.Down == 0 || usual.Down > 60*60 {
		t.Fatalf("a normal day reads as %+v", usual.Rollup)
	}
	for _, sc := range []int{index("transit-down"), index("home-offline")} {
		sim := preview.New(c, now, preview.DefaultLanguage)
		sim.SetScenario(sc, now.Add(-30*time.Minute))
		snap, e := sim.Snapshot("", now)
		if e != nil {
			t.Fatal(e)
		}
		var fared []model.Availability
		for _, cs := range snap.Checks {
			if cs.ID == id {
				fared = cs.Availability
			}
		}
		if len(fared) != 3 {
			t.Fatalf("scenario %s: %s has no availability", ids[sc], id)
		}
		day := fared[0]
		switch ids[sc] {
		case "transit-down":
			if more := day.Down - usual.Down; more < 25*60 || more > 31*60 || day.Unmeasured != 0 || fared[2].Down < day.Down {
				t.Fatalf("half an hour down reads as %+v", day.Rollup)
			}
		case "home-offline":
			if day.Unmeasured < 25*60 || view.MeasuredShare(day.Rollup) != "98%" {
				t.Fatalf("a quiet worker's checks read as measured: %+v", day.Rollup)
			}
		}
		args := []string{"preview", "--lang", preview.DefaultLanguage, "--scenario", fmt.Sprint(sc)}
		text, e := at(now, append(args, "show", "checks", id)...)
		if e != nil || !strings.Contains(text, "Last 24h: "+view.AvailabilityLine(day)+"\n") || !strings.Contains(text, "Last 30d: "+view.AvailabilityLine(fared[2])+"\n") {
			t.Fatalf("scenario %s: the evidence lacks how it fared: %v\n%s", ids[sc], e, text)
		}
		tab, e := at(now, append(args, "show", "checks")...)
		if e != nil || !strings.Contains(tab, "UP 24H") || !strings.Contains(tab, " "+view.UpShare(day.Rollup)+"\n") {
			t.Fatalf("scenario %s: the Checks tab lacks the day's share up: %v\n%s", ids[sc], e, tab)
		}
		out, e := at(now, append(args, "history", id, "--step", "1h", "--span", "6h", "--json")...)
		var rolled client.Rollups
		if e != nil || json.Unmarshal([]byte(out), &rolled) != nil || len(rolled.Slots) != 6 {
			t.Fatalf("scenario %s: rollups by the hour: %v\n%s", ids[sc], e, out)
		}
		last := rolled.Slots[len(rolled.Slots)-1]
		if rolled.Total.Unmeasured != day.Unmeasured || last.Down+last.Unmeasured < 25*60 {
			t.Fatalf("scenario %s: the last hour does not hold the last half hour: %+v, %+v", ids[sc], last, day.Rollup)
		}
	}
}
