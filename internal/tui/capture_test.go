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
// the default one, and the README's animations, which are chapters of the
// homepage's tour. `make captures` records each in the tour's format
// (<name>.<language>.json), a still as one frame, for scripts/capture.py to
// paint with the homepage's terminal renderer. Without TOPO_CAPTURES the
// test only checks that every capture renders.

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

// docClip is an animation in each README: chapters of the tour, in order.
type docClip struct {
	name     string
	chapters []string
}

var docClips = []docClip{
	// An outage arriving on the Overview, then its route and evidence.
	{name: "outage", chapters: []string{"outage", "route"}},
	// The network from two vantage points, then the three maps.
	{name: "views", chapters: []string{"vantage", "virtual", "geo", "network"}},
}

// clip is the tour in lang cut down to the chapters of c.
func clip(t *testing.T, c docClip, lang string) tour {
	out := recordTour(t, lang)
	all := out.Chapters
	out.Chapters = nil
	for _, id := range c.chapters {
		i := slices.IndexFunc(all, func(ch tourChapter) bool { return ch.ID == id })
		if i < 0 {
			t.Fatalf("%s: the tour has no chapter %q", c.name, id)
		}
		out.Chapters = append(out.Chapters, all[i])
	}
	return out
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
	write := func(name, lang string, out tour) {
		b, e := json.Marshal(out)
		if e != nil {
			t.Fatal(e)
		}
		if dir == "" {
			return
		}
		if e := os.WriteFile(filepath.Join(dir, name+"."+lang+".json"), b, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	for _, c := range docShots {
		langs := []string{preview.DefaultLanguage}
		if c.readme {
			langs = preview.Languages()
		}
		for _, lang := range langs {
			write(c.name, lang, shot(t, c, lang))
		}
	}
	for _, c := range docClips {
		for _, lang := range preview.Languages() {
			write(c.name, lang, clip(t, c, lang))
		}
	}
}
