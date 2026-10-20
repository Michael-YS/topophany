package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
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

func TestGeoCountsColoPseudoNodesAgainstTheBudget(t *testing.T) {
	// A topology just inside the node budget plus the colos a fresh check
	// places: the graph crosses the limit, so the map must say so rather
	// than lay out nodes the budget never counted.
	c := model.Config{Schema: 1, Project: "budget", Nodes: []model.Node{
		{ID: "obs", Name: "Observer", Kind: "host", Location: &model.Location{Lat: 47.61, Lon: -122.33}},
		{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare"},
	}, Observers: []model.Observer{{ID: "o", Node: "obs", Context: "test", StaleSeconds: 30}},
		Endpoints: []model.Endpoint{{ID: "web", Node: "obs", Address: "https://www.example.test/", Network: "public", Via: []string{"cf"}}},
		Checks:    []model.Check{{ID: "check", Observer: "o", Endpoint: "web", Kind: "http"}},
	}
	// 230 declared nodes plus the 32 colo nodes crosses the 256-node budget.
	for i := 0; i < 228; i++ {
		c.Nodes = append(c.Nodes, model.Node{ID: fmt.Sprintf("n%d", i), Kind: "host"})
	}
	snap := model.Snapshot{Topology: c}
	for i := 0; i < view.MaxColoNodes; i++ {
		code := fmt.Sprintf("%c%cX", 'A'+i/26, 'A'+i%26)
		id := fmt.Sprintf("check-%d", i)
		snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: id, Observer: "o", Endpoint: "web", Kind: "http"})
		snap.Checks = append(snap.Checks, model.CheckState{ID: id, Observer: "o", Health: "up", Freshness: "fresh",
			Last: &model.Observation{Facts: map[string]string{"edge_provider": "cloudflare", "edge_pop": code}}})
	}
	now := time.Now()
	snap.GeneratedAt = now
	m := &Model{Snapshot: snap, Now: now, MapMode: "geo"}
	nodes, edges, _ := m.graph()
	if got, want := len(nodes), len(c.Nodes)+view.MaxColoNodes; got != want {
		t.Fatalf("graph has %d nodes, want %d declared plus %d colos", got, len(c.Nodes), view.MaxColoNodes)
	}
	if len(c.Nodes) > maxLayoutNodes {
		t.Fatalf("the declared topology alone crosses the budget: %d", len(c.Nodes))
	}
	if len(nodes) <= maxLayoutNodes || len(edges) > maxLayoutEdges {
		t.Fatalf("budget case does not isolate the node count: %d nodes, %d edges", len(nodes), len(edges))
	}
	cv := NewCanvas(100, 30)
	m.drawGeo(cv, 0, 0, 100, 30, nodes, edges, mapFocus{})
	if !strings.Contains(securityCanvasText(cv), "Map complexity limit") {
		t.Fatal("the geo map laid out nodes past its budget")
	}
	// Without the reported colo the same topology is inside the budget and
	// draws; the pseudo-nodes are what crossed it.
	for i := range snap.Checks {
		snap.Checks[i].Freshness = "stale"
	}
	nodes, edges, _ = (&Model{Snapshot: snap, Now: now, MapMode: "geo"}).graph()
	if len(nodes) != len(c.Nodes) {
		t.Fatalf("stale evidence still placed a colo: %d nodes", len(nodes))
	}
	cv = NewCanvas(100, 30)
	(&Model{Snapshot: snap, Now: now, MapMode: "geo"}).drawGeo(cv, 0, 0, 100, 30, nodes, edges, mapFocus{})
	if strings.Contains(securityCanvasText(cv), "Map complexity limit") {
		t.Fatal("a topology inside the budget was refused")
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
		shapes[i] = plateShapes(s, nodes, mapFocus{}, 5, 20, nil, false)
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
	if ps, complete := placePlatesBudget(sites, nodes, mapFocus{}, w, h, 2, land, arcs, taken, nil, false, work); complete || ps != nil || work.remaining != 0 {
		t.Fatal("alternate label orders renewed the work budget or accepted partial costs")
	}
	if ps, complete := placePlates(sites, nodes, mapFocus{}, w, h, 2, land, arcs, taken, nil, false); !complete || len(ps) != 2 {
		t.Fatal("new render inherited the exhausted budget")
	}
}
