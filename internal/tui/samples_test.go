package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
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
