package tui

import (
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// A colo an edge reported is where it said it answered from, not a server a
// check measured. Its mark is hollow and never the up color, so "◌ FRA" is
// not read as "● DO Frankfurt" on any of the maps; a failing route keeps the
// warning every mark gives.
func TestSignalMarkKeepsReportedColosHollow(t *testing.T) {
	for _, tc := range []struct {
		sig   string
		colo  bool
		glyph rune
		color Color
	}{
		{"carried", true, '◌', coloColor},
		{"up", true, '◌', coloColor},
		{"unknown", true, '◌', cUnknown},
		{"", true, '◌', cUnknown},
		{"affected", true, '▲', cWarn},
		{"degraded", true, '▲', cWarn},
		{"down", true, '✖', cDown},
		{"up", false, '●', cUp},
		{"carried", false, '●', cUp},
		{"unknown", false, '○', cUnknown},
	} {
		g, c := signalMark(tc.sig, tc.colo)
		if g != tc.glyph || c != tc.color {
			t.Errorf("signal %q colo %v marks %q in %x, want %q in %x", tc.sig, tc.colo, g, c, tc.glyph, tc.color)
		}
	}
	if coloColor == cUp {
		t.Fatal("a reported colo wears the measured up color")
	}
	g, _ := nodeMark(mapNode{ID: "colo:cf:FRA", Kind: "colo", Signal: "carried"})
	h, _ := nodeMark(mapNode{ID: "dc", Kind: "host", Health: "up"})
	if g != '◌' || h != '●' {
		t.Fatalf("a colo is %q and a measured host %q", g, h)
	}
}

// Every map draws a reported colo hollow and a measured server filled, and
// the legend says what the hollow mark and the dotted hop mean.
func TestMapsDrawReportedColosHollow(t *testing.T) {
	for _, mode := range []string{"geo", "virtual", "network"} {
		m := previewModel(t)
		m.Width, m.Height, m.MapMode = 200, 50, mode
		m.setTab(tabTopology)
		out := visible(m)
		seen := 0
		for _, l := range strings.Split(out, "\n") {
			for _, code := range []string{"FRA", "EWR", "LAX"} {
				if !strings.Contains(l, code) {
					continue
				}
				if strings.Contains(l, "● "+code) || strings.Contains(l, "○ "+code) {
					t.Fatalf("%s map draws the reported colo %s as a server:\n%s", mode, code, l)
				}
				if strings.Contains(l, "◌ "+code) {
					seen++
				}
			}
		}
		if seen == 0 {
			t.Fatalf("%s map shows no hollow reported colo:\n%s", mode, out)
		}
		if !strings.Contains(out, "◌ reported colo  ┈ reported colo") {
			t.Fatalf("%s legend does not explain the colo marks:\n%s", mode, out)
		}
	}
}

// A site holding only reported colos is marked as hollow as the colo is; one
// holding a measured server keeps that server's mark.
func TestGeoSiteOfOnlyColosIsHollow(t *testing.T) {
	nodes := []mapNode{
		{ID: "dc", Name: "DC", Kind: "host", Health: "up", Located: true, Lat: 50, Lon: 8, Place: "Frankfurt"},
		{ID: "colo:cf:FRA", Name: "FRA", Kind: "colo", Signal: "carried", Located: true, Lat: 50.03, Lon: 8.57},
		{ID: "colo:cf:LAX", Name: "LAX", Kind: "colo", Signal: "carried", Located: true, Lat: 33.94, Lon: -118.41},
	}
	g := fitGeo([][2]float64{{50, 8}, {50.03, 8.57}, {33.94, -118.41}}, 240, 160, 2, 0, nil)
	sites := geoSites(nodes, g, mapFocus{})
	if len(sites) != 2 {
		t.Fatalf("%d sites", len(sites))
	}
	for _, s := range sites {
		glyph, _ := s.mark()
		hasHost := false
		for _, i := range s.nodes {
			hasHost = hasHost || nodes[i].Kind != "colo"
		}
		if hasHost && glyph != '●' || !hasHost && glyph != '◌' {
			t.Fatalf("site of %v is marked %q", s.nodes, glyph)
		}
	}
	if view.SignalRank("carried") != view.SignalRank("up") {
		t.Fatal("carried no longer ranks with up; the marks above assume it does")
	}
}
