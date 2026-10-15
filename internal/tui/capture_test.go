package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
)

// The captures in docs/assets are stills of the TUI on the preview, the
// README's in every language the preview is written in and the guide's in
// the default one. `make captures` records each as one frame of the tour's
// format (<name>.<language>.json) for scripts/capture.py to paint with the
// homepage's terminal renderer. Without TOPO_CAPTURES the test only checks
// that every capture renders.

type docShot struct {
	name     string
	readme   bool   // shown in each README, so captured in every language
	tab      int    // the tab shown
	mapMode  string // the map, where the tab has one
	selected string // the ID of the row selected, else the first
}

var docShots = []docShot{
	{name: "overview", readme: true, tab: tabOverview},
	{name: "network", tab: tabTopology, mapMode: "network", selected: "transit"},
	{name: "incident", tab: tabIncidents},
}

// shot is the TUI as it shows c in lang: half an hour into the transit
// outage, four seconds after the last snapshot.
func shot(t *testing.T, c docShot, lang string) tour {
	r := newRecorder(t, lang)
	m := r.m
	r.sim.SetScenario(slices.Index(r.sim.ScenarioIDs(), "transit-blocked"), m.Now.Add(-30*time.Minute))
	r.fetch()
	m.Now = m.Now.Add(4 * time.Second)
	m.notice = ""
	m.setTab(c.tab)
	m.MapMode = c.mapMode
	if c.selected != "" {
		m.Selected = slices.IndexFunc(m.Rows(), func(row Row) bool { return row.ID == c.selected })
		if m.Selected < 0 {
			t.Fatalf("%s: no row %q", c.name, c.selected)
		}
	}
	r.chapter(c.name)
	r.shoot(0)
	return r.out
}

func TestCaptures(t *testing.T) {
	dir := os.Getenv("TOPO_CAPTURES")
	local := time.Local
	time.Local = time.UTC
	defer func() { time.Local = local }()
	for _, c := range docShots {
		langs := []string{preview.DefaultLanguage}
		if c.readme {
			langs = preview.Languages()
		}
		for _, lang := range langs {
			b, e := json.Marshal(shot(t, c, lang))
			if e != nil {
				t.Fatal(e)
			}
			if dir == "" {
				continue
			}
			if e := os.WriteFile(filepath.Join(dir, c.name+"."+lang+".json"), b, 0o644); e != nil {
				t.Fatal(e)
			}
		}
	}
}
