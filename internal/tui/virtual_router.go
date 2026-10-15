package tui

import (
	"math"
)

// vRouter lays wires on the cell grid by shortest path: steps cost, bends
// cost more, and a wire crosses another only at right angles, never running
// along it or turning on it. Wires from one source may share cells, and so
// may wires into one destination, so they branch and merge like buses
// without ever joining two other nodes. Beside a card, the cell next to each
// row is that row's port and is closed to every other wire; in the port and
// the cell beyond it, a node's wires in and out may meet.
type vRouter struct {
	w, h  int
	solid []bool
	port  []int32 // -1 open, -2 closed, otherwise the port this cell serves
	zone  []int32 // the port whose wires may meet here, or -1
	near  []bool  // just above or below a card
	ws    *wires
	ends  [][2]int32 // the ports each routed edge joins
	g     []int32
	stamp []int32
	prev  []int32
	gen   int32
	heap  []vItem

	work *layoutWork
}

type vItem struct{ f, s int32 }

func newRouter(w, h int) *vRouter {
	n := w * h
	r := &vRouter{w: w, h: h, solid: make([]bool, n), port: make([]int32, n), zone: make([]int32, n), near: make([]bool, n), ws: newWires(w, h),
		g: make([]int32, 2*n), stamp: make([]int32, 2*n), prev: make([]int32, 2*n), work: newLayoutWork()}
	for i := range r.port {
		r.port[i], r.zone[i] = -1, -1
	}
	return r
}

// kin reports whether edges a and b may share cell c: they leave the same
// port or enter the same one, or c is where one's end meets the other's.
func (r *vRouter) kin(a, b, c int) bool {
	x, y := r.ends[a], r.ends[b]
	if x[0] == y[0] || x[1] == y[1] {
		return true
	}
	z := r.zone[c]
	return z >= 0 && (x[0] == z || x[1] == z) && (y[0] == z || y[1] == z)
}

// enter is the cost for edge e of stepping into cell c along axis ax (0
// across, 1 up or down), or -1 where it may not go.
func (r *vRouter) enter(c, ax, e int) int32 {
	if r.solid[c] {
		return -1
	}
	if p := r.port[c]; p == -2 || p >= 0 && (ax == 1 || p != r.ends[e][0] && p != r.ends[e][1]) {
		return -1
	}
	along := wE | wW
	if ax == 1 {
		along = wN | wS
	}
	cross, shared := false, false
	for _, mk := range r.ws.marks[c] {
		if !r.work.take() {
			return -1
		}
		if r.kin(e, mk.edge, c) {
			shared = shared || mk.dirs&along != 0
			continue
		}
		if mk.dirs != along^(wN|wS|wE|wW) {
			return -1
		}
		cross = true
	}
	cost := int32(2)
	switch {
	case cross:
		cost = 8
	case shared:
		cost = 1
	}
	if r.near[c] {
		cost++
	}
	return cost
}

// turnable reports whether edge e may turn in cell c: only where every wire
// already there shares an end with it, which makes the turn a branch.
func (r *vRouter) turnable(c, e int) bool {
	for _, mk := range r.ws.marks[c] {
		if !r.work.take() || !r.kin(e, mk.edge, c) {
			return false
		}
	}
	return true
}

func (r *vRouter) push(it vItem) {
	r.heap = append(r.heap, it)
	i := len(r.heap) - 1
	for i > 0 {
		p := (i - 1) / 2
		if r.heap[p].f <= it.f {
			break
		}
		r.heap[i] = r.heap[p]
		i = p
	}
	r.heap[i] = it
}

func (r *vRouter) pop() vItem {
	top, last := r.heap[0], r.heap[len(r.heap)-1]
	r.heap = r.heap[:len(r.heap)-1]
	n := len(r.heap)
	if n == 0 {
		return top
	}
	i := 0
	for {
		k := 2*i + 1
		if k >= n {
			break
		}
		if k+1 < n && r.heap[k+1].f < r.heap[k].f {
			k++
		}
		if r.heap[k].f >= last.f {
			break
		}
		r.heap[i] = r.heap[k]
		i = k
	}
	r.heap[i] = last
	return top
}

// path finds the cheapest wire for edge e from any cell of srcs to any cell
// of dsts, leaving and arriving across, as the cells it passes in order; nil
// when there is none or when the shared render budget is exhausted.
func (r *vRouter) path(srcs, dsts []int, e int) []int {
	if len(srcs) == 0 || len(dsts) == 0 || r.work.remaining <= 0 {
		return nil
	}
	r.gen++
	r.heap = r.heap[:0]
	goal := func(c int) bool {
		for _, d := range dsts {
			if d == c {
				return true
			}
		}
		return false
	}
	est := func(c int) int32 {
		best := math.MaxInt32
		for _, d := range dsts {
			best = min(best, abs(d%r.w-c%r.w)+abs(d/r.w-c/r.w))
		}
		return int32(best)
	}
	for _, c := range srcs {
		s := int32(2 * c)
		r.stamp[s], r.g[s], r.prev[s] = r.gen, 0, -1
		r.push(vItem{est(c), s})
	}
	for budget := 8 * r.w * r.h; len(r.heap) > 0 && budget > 0 && r.work.take(); budget-- {
		it := r.pop()
		s := int(it.s)
		c, ax := s/2, s%2
		g := r.g[s]
		if it.f > g+est(c) {
			continue
		}
		if goal(c) {
			cells := []int{}
			for k := int32(s); k >= 0; k = r.prev[k] {
				cells = append(cells, int(k)/2)
			}
			for i, j := 0, len(cells)-1; i < j; i, j = i+1, j-1 {
				cells[i], cells[j] = cells[j], cells[i]
			}
			return cells
		}
		cx, cy := c%r.w, c/r.w
		for d := 0; d < 4; d++ {
			nx, ny, nax := cx, cy, d/2
			switch d {
			case 0:
				nx++
			case 1:
				nx--
			case 2:
				ny++
			case 3:
				ny--
			}
			if nx < 0 || ny < 0 || nx >= r.w || ny >= r.h {
				continue
			}
			nc := ny*r.w + nx
			cost := r.enter(nc, nax, e)
			if cost < 0 {
				continue
			}
			if nax != ax {
				if r.port[c] >= 0 || !r.turnable(c, e) {
					continue
				}
				cost += 5
			}
			ns := int32(2*nc + nax)
			if ng := g + cost; r.stamp[ns] != r.gen || ng < r.g[ns] {
				r.stamp[ns], r.g[ns], r.prev[ns] = r.gen, ng, int32(s)
				r.push(vItem{ng + est(nc), ns})
			}
		}
	}
	return nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// corners reduces a run of cells to its ends and the cells where it turns.
func corners(cells []int, w int) [][2]int {
	pts := make([][2]int, 0, 8)
	for k, c := range cells {
		if k > 0 && k < len(cells)-1 && (cells[k-1]/w == c/w) == (c/w == cells[k+1]/w) {
			continue
		}
		pts = append(pts, [2]int{c % w, c / w})
	}
	return pts
}
