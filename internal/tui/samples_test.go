package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A check's evidence shows its recent samples: they are read when the check
// is selected, again at a refresh only when the snapshot holds a newer
// sample or the last read failed, and drawn as a strip over the evidence,
// newest at the right. A failed read keeps what was read before.
func TestCheckEvidenceShowsRecentSamples(t *testing.T) {
	m := previewModel(t)
	sim := m.Preview.(*preview.Sim)
	if m.wantSamples(true) != "" {
		t.Fatal("samples read off the Checks tab")
	}
	m.setTab(tabChecks)
	id := selectFirstEntry(t, m).ID
	if got := m.wantSamples(false); got != id {
		t.Fatalf("selected check %s, samples wanted for %q", id, got)
	}
	if !strings.Contains(visible(m), "reading…") {
		t.Fatal("evidence does not say its samples are being read")
	}
	entries, e := sim.History(id, view.StripSamples, m.Now)
	if e != nil || len(entries) == 0 {
		t.Fatal(e)
	}
	m.KeepSamples(id, entries, nil)
	if got := m.wantSamples(false); got != "" {
		t.Fatalf("samples read again for %q", got)
	}
	if got := m.wantSamples(true); got != "" {
		t.Fatalf("samples refreshed for %q with nothing newer", got)
	}
	out := visible(m)
	x, _ := config.Check(m.Snapshot.Topology, id)
	strip := view.NewStrip(x, entries, m.Now, 40)
	if !strings.Contains(out, "RECENT SAMPLES") || !strings.Contains(out, view.Glyphs(strip.Slots)) || !strings.Contains(out, strip.Latency.String()) {
		t.Fatalf("evidence lacks the strip %q:\n%s", view.Glyphs(strip.Slots), out)
	}
	m.KeepSamples(id, nil, errors.New("collector unreachable"))
	if out := visible(m); !strings.Contains(out, view.Glyphs(strip.Slots)) || !strings.Contains(out, "Not refreshed: collector unreachable") {
		t.Fatalf("a failed read dropped the samples held:\n%s", out)
	}
	if m.wantSamples(false) != "" || m.wantSamples(true) != id {
		t.Fatal("a failed read is retried only at a refresh")
	}
	m.Key("down")
	for m.Rows()[m.Selected].Kind == "group" {
		m.Key("down") // past the next group's header, to its first check
	}
	next := m.Rows()[m.Selected].ID
	if m.wantSamples(false) != next {
		t.Fatal("moving to another check does not read its samples")
	}
	m.KeepSamples(next, nil, errors.New("collector unreachable"))
	if out := visible(m); !strings.Contains(out, "unavailable: collector unreachable") {
		t.Fatalf("a failed first read is not said:\n%s", out)
	}
	m.Detail = true
	if m.KeepSamples(next, nil, nil); !strings.Contains(visible(m), "none in the collector's history") {
		t.Fatal("a check with no samples is not said")
	}
}

// A check's evidence adds the last day's first-byte time by cache class
// and the share of answers by colo, when the collector's rollups hold them,
// after the overall latency and before the legend. Both lines name their
// window, so the pair reads together, and the colos carry their label.
func TestCheckEvidenceShowsFirstByteAndColos(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabChecks)
	target := ""
	var day *model.Rollup
	for i := range m.Snapshot.Checks {
		c := &m.Snapshot.Checks[i]
		if r := view.RollupWindow(c.Availability, "24h"); r != nil && r.FirstByte != nil && len(r.Pops) > 0 {
			target, day = c.ID, r
			break
		}
	}
	if target == "" {
		t.Fatal("no preview check carries first-byte and colo rollups")
	}
	sim := m.Preview.(*preview.Sim)
	entries, e := sim.History(target, view.StripSamples, m.Now)
	if e != nil || len(entries) == 0 {
		t.Fatal(e)
	}
	m.KeepSamples(target, entries, nil)
	for i, r := range m.Rows() {
		if r.ID == target {
			m.Selected = i
		}
	}
	m.Detail = true
	out := visible(m)
	first := "first byte " + view.FirstByteLine(day.FirstByte) + " · 24h"
	colos := "colos " + view.PopsLine(day.Pops, 4) + " · 24h"
	if !strings.Contains(out, first) || !strings.Contains(out, colos) {
		t.Fatalf("evidence lacks first byte %q or colos %q:\n%s", first, colos, out)
	}
	lines := func(r Row, w int) []string {
		out := []string{}
		for _, l := range m.sampleLines(r, w) {
			text := ""
			for _, sp := range l {
				text += sp.s
			}
			out = append(out, text)
		}
		return out
	}
	var row Row
	for _, r := range m.Rows() {
		if r.ID == target {
			row = r
		}
	}
	// A known day: what the lines say, and where they break at the widths
	// of a narrow panel, never leaving the window alone on a line.
	for i := range m.Snapshot.Checks {
		if m.Snapshot.Checks[i].ID == target {
			m.Snapshot.Checks[i].Availability = []model.Availability{{Window: "24h", Rollup: model.Rollup{
				FirstByte: &model.FirstByteLatency{Origin: &model.Latency{Samples: 900, P50: 98, P95: 126, Max: 300}},
				Pops:      map[string]int64{"SJC": 970, "LAX": 20, "other": 10},
			}}}
		}
	}
	for w, want := range map[int][]string{
		60: {"first byte origin p50 98 ms · p95 126 ms · 24h", "colos SJC 97% · LAX 2% · 24h"},
		44: {"first byte origin p50 98 ms", "p95 126 ms · 24h", "colos SJC 97% · LAX 2% · 24h"},
		30: {"first byte origin p50 98 ms", "p95 126 ms · 24h", "colos SJC 97% · LAX 2% · 24h"},
		18: {"first byte origin", "p50 98 ms", "p95 126 ms · 24h", "colos SJC 97%", "LAX 2% · 24h"},
	} {
		got := lines(row, w)
		at := slices.Index(got, want[0])
		if at < 0 || at+len(want) > len(got) || !slices.Equal(got[at:at+len(want)], want) {
			t.Fatalf("at %d columns the evidence reads %q, want %q", w, got, want)
		}
		for _, l := range got {
			if l == "24h" || strings.HasPrefix(l, "· ") {
				t.Fatalf("at %d columns the window stands alone: %q", w, l)
			}
		}
	}
}
