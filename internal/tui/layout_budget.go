package tui

import (
	"slices"
	"sort"
)

const (
	maxLayoutNodes  = 256
	maxLayoutEdges  = 512
	maxLayoutWidth  = 512
	maxLayoutHeight = 256
	maxLayoutCells  = 32768
	maxLayoutWork   = 200000
)

// Structural limits are deliberately separate from configuration acceptance:
// large topologies remain available in lists/details, but cannot monopolize the
// synchronous event loop with optional diagram layout and routing.
func mapGraphAllowed(w, nodes, edges int) bool {
	return w > 0 && w <= maxLayoutWidth && nodes >= 0 && nodes <= maxLayoutNodes && edges >= 0 && edges <= maxLayoutEdges
}

func mapLayoutAllowed(w, h, nodes, edges int) bool {
	return mapGraphAllowed(w, nodes, edges) && h > 0 && h <= maxLayoutHeight && w <= maxLayoutCells/h
}

func drawLayoutLimit(cv *Canvas, x, y, w, h, nodes, edges int) {
	if w <= 0 || h <= 0 {
		return
	}
	cv.Text(x, y, "Map complexity limit", fg(cWarn), w)
	if h > 1 {
		cv.Text(x, y+1, itoa(nodes)+" nodes / "+itoa(edges)+" edges; diagram omitted", fg(cMuted), w)
	}
	if h > 2 {
		cv.Text(x, y+2, "Topology remains available in the list and details.", fg(cMuted), w)
	}
}

// A single work counter covers every candidate, edge, retry and alternate
// layout in an operation. Exhaustion must never reset it or accept partial costs.
// Work is counted instead of timing the machine, so tests and output are stable.
// The counter is local to a render, never shared between models or goroutines.
type layoutWork struct{ remaining int }

func newLayoutWork() *layoutWork { return &layoutWork{remaining: maxLayoutWork} }
func (b *layoutWork) take() bool {
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

type networkBus struct {
	src, ys, lo, hi int
	targets         map[int]int
}

func networkBusCost(order []*networkBus, work *layoutWork) (int, bool) {
	n := 0
	for i, a := range order {
		for j, b := range order {
			if !work.take() {
				return 0, false
			}
			meet := func(y, target int) {
				switch v, ok := b.targets[y]; {
				case y < b.lo || y > b.hi:
				case ok && v == target:
				case ok || y == b.ys:
					n += 4
				default:
					n++
				}
			}
			switch {
			case j < i:
				meet(a.ys, -1)
			case j > i:
				for y, v := range a.targets {
					if !work.take() {
						return 0, false
					}
					meet(y, v)
				}
			}
		}
	}
	return n, true
}

func orderNetworkBuses(list []*networkBus, work *layoutWork) []*networkBus {
	// Maps feed this list, so equal source rows need a deterministic tie-break.
	list = slices.Clone(list)
	sort.Slice(list, func(i, j int) bool {
		if list[i].ys == list[j].ys {
			return list[i].src < list[j].src
		}
		return list[i].ys < list[j].ys
	})
	best := slices.Clone(list)
	bestCost, complete := networkBusCost(best, work)
	if !complete {
		return best
	}
	if len(list) <= 6 {
		var permute func(int)
		permute = func(k int) {
			if work.remaining == 0 {
				return
			}
			if k == len(list) {
				if c, ok := networkBusCost(list, work); ok && c < bestCost {
					best, bestCost = slices.Clone(list), c
				}
				return
			}
			for i := k; i < len(list) && work.remaining > 0; i++ {
				list[k], list[i] = list[i], list[k]
				permute(k + 1)
				list[k], list[i] = list[i], list[k]
			}
		}
		permute(0)
	} else {
		for improved := true; improved && work.remaining > 0; {
			improved = false
			for i := 0; i+1 < len(best) && work.remaining > 0; i++ {
				best[i], best[i+1] = best[i+1], best[i]
				if c, ok := networkBusCost(best, work); ok && c < bestCost {
					bestCost, improved = c, true
				} else {
					best[i], best[i+1] = best[i+1], best[i]
				}
			}
		}
	}
	return best
}

func (l *vLayout) route() [][]int { return l.routeBudget(newLayoutWork()) }
