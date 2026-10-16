package tui

import (
	"strings"
	"testing"
)

// An empty list says why it is empty and how to fill it; a filter lights
// up where it matches each name.
func TestListsSayWhyEmptyAndLightTheFilter(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height, m.Colors = 140, 42, colorTrue
	m.setTab(tabChecks)
	m.Search = "no-such-check"
	if out := visible(m); !strings.Contains(out, "Nothing matches “no-such-check”.") || !strings.Contains(out, "Esc clears the filter.") {
		t.Fatal("an empty filtered list does not say why:\n", out)
	}
	m.Search = "ping"
	cv := m.draw()
	lit := 0
	for _, c := range cv.cells {
		if c.st.BG == Mix(cAccent, cInk, .55) {
			lit++
		}
	}
	if lit == 0 || lit%4 != 0 {
		t.Fatalf("the filter lights %d cells, not each match of its four letters", lit)
	}
	// A table drops a column in which every row says the same word.
	m.Search = ""
	lay := layoutTable(m.Rows(), m.tabColumns(), 120)
	for _, i := range lay.idx {
		if m.tabColumns()[i+1] == "FRESHNESS" {
			t.Fatal("a column of the same word on every row kept")
		}
	}
}
