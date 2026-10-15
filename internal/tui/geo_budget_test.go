package tui

import (
	"strings"
	"testing"
)

func TestGeoRejectsOversizedViewportAndEdges(t *testing.T) {
	for _, tc := range []struct{ w, h, edges int }{
		{maxLayoutWidth + 1, 20, 0},
		{100, maxLayoutHeight + 1, 0},
		{maxLayoutWidth, maxLayoutCells/maxLayoutWidth + 1, 0},
		{100, 30, maxLayoutEdges + 1},
	} {
		m := &Model{MapMode: "geo"}
		cv := NewCanvas(tc.w, tc.h)
		m.drawGeo(cv, 0, 0, tc.w, tc.h, nil, make([]mapEdge, tc.edges), mapFocus{})
		if !strings.Contains(securityCanvasText(cv), "Map complexity limit") {
			t.Fatal("geographic renderer bypassed structural limits", tc)
		}
	}
}

func TestGeoPlateWorkIsSharedAcrossOrders(t *testing.T) {
	const w, h = 60, 20
	nodes := []mapNode{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	sites := []*geoSite{{x: 30, y: 40, nodes: []int{0}}, {x: 80, y: 40, nodes: []int{1}}}
	land := make([]uint8, w*h)
	arcs, taken := make([]bool, w*h), make([]bool, w*h)
	shapes := make([][]plate, len(sites))
	for i, s := range sites {
		shapes[i] = plateShapes(s, nodes, mapFocus{}, 5, 20, nil)
	}
	empty := newSat(w, h, func(int, int) int32 { return 0 })
	work := newLayoutWork()
	ps, _, complete := placeInOrder(sites, shapes, []int{0, 1}, w, h, 2, empty, empty, taken, work)
	if !complete || len(ps) != 2 {
		t.Fatal("ordinary placement failed")
	}
	used := maxLayoutWork - work.remaining
	// Enough for the static tables and one complete order, but not all four.
	work = &layoutWork{remaining: 2*w*h + used + 1}
	if ps, complete := placePlatesBudget(sites, nodes, mapFocus{}, w, h, 2, land, arcs, taken, nil, work); complete || ps != nil || work.remaining != 0 {
		t.Fatal("alternate label orders renewed the work budget or accepted partial costs")
	}
	if ps, complete := placePlates(sites, nodes, mapFocus{}, w, h, 2, land, arcs, taken, nil); !complete || len(ps) != 2 {
		t.Fatal("new render inherited the exhausted budget")
	}
}
