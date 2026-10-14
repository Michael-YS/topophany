package tui

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestSecurityLayoutAdmissionBoundaries(t *testing.T) {
	big := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name       string
		w, h, n, e int
		allowed    bool
	}{
		{"empty", 80, 24, 0, 0, true},
		{"exact_graph", 80, 24, maxLayoutNodes, maxLayoutEdges, true},
		{"node_over", 80, 24, maxLayoutNodes + 1, 0, false},
		{"edge_over", 80, 24, 1, maxLayoutEdges + 1, false},
		{"width_exact", maxLayoutWidth, 1, 1, 0, true},
		{"width_over", maxLayoutWidth + 1, 1, 1, 0, false},
		{"height_exact", 1, maxLayoutHeight, 1, 0, true},
		{"height_over", 1, maxLayoutHeight + 1, 1, 0, false},
		{"cells_exact", 256, 128, 1, 0, true},
		{"cells_over", 257, 128, 1, 0, false},
		{"zero_width", 0, 24, 1, 0, false},
		{"zero_height", 80, 0, 1, 0, false},
		{"negative_width", -1, 24, 1, 0, false},
		{"negative_height", 80, -1, 1, 0, false},
		{"negative_nodes", 80, 24, -1, 0, false},
		{"negative_edges", 80, 24, 1, -1, false},
		{"integer_overflow", big, big, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapLayoutAllowed(tc.w, tc.h, tc.n, tc.e); got != tc.allowed {
				t.Fatalf("admission=%v, want %v", got, tc.allowed)
			}
		})
	}
	work := &layoutWork{remaining: 1}
	if !work.take() || work.take() || work.take() || work.remaining != 0 {
		t.Fatal("work counter underflowed or renewed itself")
	}
}

func TestSecurityNetworkCostSemantics(t *testing.T) {
	bus := func(src, y, target, id int) *networkBus {
		return &networkBus{src: src, ys: y, lo: min(y, target), hi: max(y, target), targets: map[int]int{target: id}}
	}
	for _, tc := range []struct {
		name  string
		buses []*networkBus
		cost  int
	}{
		{"empty", nil, 0},
		{"single", []*networkBus{bus(0, 0, 6, 10)}, 0},
		{"disjoint", []*networkBus{bus(0, 0, 2, 10), bus(1, 4, 6, 11)}, 0},
		{"crossing", []*networkBus{bus(0, 0, 6, 10), bus(1, 3, 8, 11)}, 2},
		{"same_destination", []*networkBus{bus(0, 0, 6, 10), bus(1, 3, 6, 10)}, 1},
		{"overlapping_runs", []*networkBus{bus(0, 0, 3, 10), bus(1, 3, 8, 11)}, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, complete := networkBusCost(tc.buses, newLayoutWork())
			if !complete || got != tc.cost {
				t.Fatalf("cost=%d complete=%v; want %d", got, complete, tc.cost)
			}
		})
	}
	if _, complete := networkBusCost([]*networkBus{bus(0, 0, 6, 10)}, &layoutWork{}); complete {
		t.Fatal("exhausted work was accepted as a complete zero-cost candidate")
	}
}

func securityBuses(n int) []*networkBus {
	list := make([]*networkBus, n)
	for i := range list {
		list[i] = &networkBus{src: i, ys: i % 5, lo: 0, hi: n + 7, targets: map[int]int{
			(i*7 + 3) % (n + 7):  i,
			(i*11 + 5) % (n + 7): i + n,
		}}
	}
	return list
}

func TestSecurityNetworkOrderingBudgetAndDeterminism(t *testing.T) {
	for _, n := range []int{1, 3, 6, 7, 64, maxLayoutEdges} {
		for _, budget := range []int{0, 1, 37, 10000} {
			t.Run(fmt.Sprintf("buses=%d/budget=%d", n, budget), func(t *testing.T) {
				list := securityBuses(n)
				before := slices.Clone(list)
				work := &layoutWork{remaining: budget}
				got := orderNetworkBuses(list, work)
				if !slices.Equal(list, before) || len(got) != len(list) {
					t.Fatal("ordering mutated input or omitted a bus")
				}
				seen := make(map[*networkBus]bool)
				for _, b := range got {
					if !slices.Contains(list, b) || seen[b] {
						t.Fatal("ordering is not a permutation")
					}
					seen[b] = true
				}
				if work.remaining < 0 || work.remaining > budget {
					t.Fatal("work budget was exceeded or renewed")
				}
				// Equal rows must not inherit randomized map iteration order.
				slices.Reverse(list)
				again := orderNetworkBuses(list, &layoutWork{remaining: budget})
				if !slices.Equal(got, again) {
					t.Fatal("same buses produced a different bounded ordering")
				}
				left := work.remaining
				orderNetworkBuses(list, work)
				if work.remaining > left {
					t.Fatal("the next layer gap received a fresh budget")
				}
			})
		}
	}
}

func TestSecurityNetworkSmallGraphKeepsOptimalOrdering(t *testing.T) {
	list := securityBuses(4)
	best := int(^uint(0) >> 1)
	// Independent enumeration checks the optimizer against every possible
	// complete order, not merely against the optimizer's own initial order.
	for a := range list {
		for b := range list {
			for c := range list {
				for d := range list {
					if a == b || a == c || a == d || b == c || b == d || c == d {
						continue
					}
					cost, ok := networkBusCost([]*networkBus{list[a], list[b], list[c], list[d]}, newLayoutWork())
					if !ok {
						t.Fatal("small reference cost exhausted the budget")
					}
					best = min(best, cost)
				}
			}
		}
	}
	got, ok := networkBusCost(orderNetworkBuses(list, newLayoutWork()), newLayoutWork())
	if !ok || got != best {
		t.Fatalf("small-graph quality regressed: cost=%d, optimum=%d", got, best)
	}
}

func securityVirtualLayout() *vLayout {
	return &vLayout{
		w: 40, h: 10, cols: 2,
		cards:  []*vCard{{x: 3, y: 2, w: 4, h: 3}, {x: 30, y: 2, w: 4, h: 3}},
		cardOf: []int{0, 1}, rowOf: []int{1, 1}, portOf: []int32{0, 1},
		drawn: []vEdge{{a: 0, b: 1, pa: 0, pb: 1, ca: 0, cb: 1}},
	}
}

func TestSecurityVirtualBudgetExhaustionAndReuse(t *testing.T) {
	l := securityVirtualLayout()
	for _, budget := range []int{0, 1} {
		work := &layoutWork{remaining: budget}
		paths := l.routeBudget(work)
		if len(paths) != len(l.drawn) || unrouted(paths) != len(l.drawn) || work.remaining != 0 {
			t.Fatalf("partial routing was reported as success: paths=%v work=%d", paths, work.remaining)
		}
		// Represents the alternate-column attempt using the SAME render budget.
		if again := l.routeBudget(work); unrouted(again) != len(l.drawn) || work.remaining != 0 {
			t.Fatal("retry or alternate layout renewed exhausted work")
		}
	}
	if paths := l.routeBudget(nil); len(paths) != len(l.drawn) || unrouted(paths) != len(paths) {
		t.Fatal("nil budget did not fail closed")
	}
	oversized := *l
	oversized.w = maxLayoutWidth + 1
	if paths := oversized.route(); unrouted(paths) != len(l.drawn) {
		t.Fatal("oversized grid reached routing")
	}
}

func TestSecurityVirtualNormalRoutesRemainCompleteAndSafe(t *testing.T) {
	l := securityVirtualLayout()
	paths := l.route()
	if len(paths) != 1 || len(paths[0]) == 0 {
		t.Fatal("ordinary graph lost its route")
	}
	p := paths[0]
	if !slices.Contains(l.ports(0), p[0]) || !slices.Contains(l.ports(1), p[len(p)-1]) {
		t.Fatal("route does not connect the declared ports")
	}
	r := l.router()
	for i, c := range p {
		if c < 0 || c >= l.w*l.h || r.solid[c] {
			t.Fatal("route escaped the grid or entered a card")
		}
		if i > 0 && abs(c%l.w-p[i-1]%l.w)+abs(c/l.w-p[i-1]/l.w) != 1 {
			t.Fatal("route contains a disconnected step")
		}
	}
	if again := l.route(); !reflect.DeepEqual(paths, again) {
		t.Fatal("identical layout produced nondeterministic paths")
	}
}

func TestSecurityVirtualPathEmptyBlockedAndSharedWork(t *testing.T) {
	r := newRouter(16, 8)
	r.ends = [][2]int32{{0, 1}}
	before := r.work.remaining
	if r.path(nil, []int{1}, 0) != nil || r.path([]int{1}, nil, 0) != nil || r.work.remaining != before {
		t.Fatal("empty endpoints consumed search work")
	}
	for y := 0; y < r.h; y++ {
		r.solid[y*r.w+8] = true
	}
	r.work = &layoutWork{remaining: 7}
	if r.path([]int{17}, []int{30}, 0) != nil || r.work.remaining != 0 {
		t.Fatal("blocked path did not honor the shared work cap")
	}
	if r.path([]int{17}, []int{18}, 0) != nil || r.work.remaining != 0 {
		t.Fatal("next edge renewed work after exhaustion")
	}
}

func securityCanvasText(cv *Canvas) string {
	var out strings.Builder
	for _, c := range cv.cells {
		out.WriteString(c.ch)
	}
	return out.String()
}

func TestSecurityLargeMapsDegradeWithoutDiscardingTopology(t *testing.T) {
	for _, mode := range []string{"network", "virtual"} {
		for _, n := range []int{maxLayoutNodes + 1, 3000} {
			t.Run(fmt.Sprintf("%s/%d", mode, n), func(t *testing.T) {
				m := &Model{MapMode: mode}
				nodes := make([]mapNode, n)
				for i := range nodes {
					nodes[i] = mapNode{ID: fmt.Sprintf("n%d", i), Name: "node", Located: true, Lat: float64(i%80), Lon: float64(i%170)}
					m.Snapshot.Topology.Nodes = append(m.Snapshot.Topology.Nodes, model.Node{ID: nodes[i].ID, Name: "node", Location: &model.Location{Lat: nodes[i].Lat, Lon: nodes[i].Lon}})
				}
				before := slices.Clone(nodes)
				cv := NewCanvas(100, 30)
				if mode == "virtual" {
					if m.virtualRows(100) != 0 || m.layoutVirtual(100, 30, 0, nodes, nil, mapFocus{}) != nil {
						t.Fatal("large graph entered virtual measurement/layout")
					}
					m.drawVirtual(cv, 0, 0, 100, 30, nodes, nil, nil, mapFocus{})
				} else {
					m.drawNetwork(cv, 0, 0, 100, 30, nodes, nil, nil, mapFocus{})
				}
				text := securityCanvasText(cv)
				if !strings.Contains(text, "Map complexity limit") || !strings.Contains(text, "list and details") {
					t.Fatal("large topology was silently truncated instead of explaining the fallback")
				}
				if !reflect.DeepEqual(nodes, before) || len(m.Snapshot.Topology.Nodes) != n {
					t.Fatal("fallback discarded or mutated topology")
				}
				// Recovery: the next, small graph must not inherit a latched limit.
				cv = NewCanvas(100, 30)
				if mode == "virtual" {
					m.drawVirtual(cv, 0, 0, 100, 30, nodes[:1], nil, nil, mapFocus{})
				} else {
					m.drawNetwork(cv, 0, 0, 100, 30, nodes[:1], nil, nil, mapFocus{})
				}
				if strings.Contains(securityCanvasText(cv), "Map complexity limit") {
					t.Fatal("ordinary topology remained stuck in fallback")
				}
			})
		}
	}
}

func BenchmarkSecurityNetworkBusBudget(b *testing.B) {
	for _, n := range []int{6, 64, 512} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			list := securityBuses(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				orderNetworkBuses(list, newLayoutWork())
			}
		})
	}
}

func BenchmarkSecurityVirtualRoutingBudget(b *testing.B) {
	m := crowdedModel()
	nodes, edges, _ := m.graph()
	l := m.layoutVirtual(96, 38, 0, nodes, edges, mapFocus{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.route()
	}
}
