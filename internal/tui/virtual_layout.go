package tui

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
)

// vPack stacks a column's cards in order of the row each wants, gap rows
// apart at least, keeping each as near to its wish as the others allow.
func vPack(list []*vCard, h, gap int) {
	if len(list) == 0 {
		return
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].want < list[j].want })
	ws := make([]float64, len(list))
	gaps := make([]float64, len(list))
	for k, c := range list {
		ws[k] = c.want
		if k > 0 {
			gaps[k] = float64(list[k-1].h + gap)
		}
	}
	packRows(ws, gaps, 0, float64(h-list[len(list)-1].h))
	for k, c := range list {
		c.y = max(0, int(math.Round(ws[k])))
		if k > 0 {
			c.y = max(c.y, list[k-1].y+list[k-1].h+gap)
		}
	}
	for k := len(list) - 1; k >= 0; k-- {
		lim := h - list[k].h
		if k+1 < len(list) {
			lim = list[k+1].y - list[k].h - gap
		}
		list[k].y = max(0, min(list[k].y, lim))
	}
}

// vCache keeps the virtual map's routed wires between frames.
type vCache struct {
	key     uint64
	cols    int     // the column limit the wires were laid out with
	paths   [][]int // cells of each drawn edge's wire, nil where none fits
	dropped int     // edges with no wire
}

type vEdge struct {
	e      mapEdge
	a, b   int   // node indices
	pa, pb int32 // their ports
	ca, cb int   // their cards
}

// vLayout is the virtual map laid out in an area: its cards, where each node
// sits on them, and the wires between cards in the order they are routed.
type vLayout struct {
	w, h   int
	cols   int
	cards  []*vCard
	cardOf []int   // node -> card
	rowOf  []int   // node -> row on its card; folded rows share the foot
	portOf []int32 // node -> port: one per card row
	drawn  []vEdge
}

// layoutVirtual lays the map out in a w×h area in at most maxCols columns
// (0 for as many as fit), or returns nil when no node has a location or when
// structural limits would make optional layout unsafe in the event loop.
func (m *Model) layoutVirtual(w, h, maxCols int, nodes []mapNode, edges []mapEdge, f mapFocus) *vLayout {
	// virtualRows uses a tall measurement area but never allocates a grid.
	if !mapGraphAllowed(w, len(nodes), len(edges)) || h <= 0 || h > 1<<12 {
		return nil
	}
	cards := virtualCards(nodes)
	var places []*vCard
	pts := [][2]float64{}
	for _, c := range cards {
		if c.located {
			places = append(places, c)
			pts = append(pts, [2]float64{c.lat, c.lon})
		}
	}
	if len(places) == 0 {
		return nil
	}
	idx := map[string]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}
	cardOf := make([]int, len(nodes))
	for k, c := range cards {
		for _, i := range c.rows {
			cardOf[i] = k
		}
	}
	// What the map draws: every declared link, and the hops of declared
	// paths when they fail, are selected or belong to the observer in view.
	// Links between two nodes on one card are drawn inside it.
	drawn := []vEdge{}
	for _, e := range edges {
		a, okA := idx[e.From]
		b, okB := idx[e.To]
		if !okA || !okB || a == b {
			continue
		}
		route := f.hops[[2]string{e.From, e.To}] || f.hops[[2]string{e.To, e.From}]
		if e.Kind == edgePath && !route && signalRank(e.Signal) < 3 && (m.Observer == "" || e.Observer != m.Observer) {
			continue
		}
		if cardOf[a] == cardOf[b] {
			if e.Kind != edgeContain {
				c, pri, _ := edgeStyle(e, f)
				cards[cardOf[a]].links = append(cards[cardOf[a]].links, vLink{a, b, c, pri})
			}
			continue
		}
		drawn = append(drawn, vEdge{e: e, a: a, b: b, ca: cardOf[a], cb: cardOf[b]})
	}

	// Columns run west to east along the shortest arc of longitude holding
	// every place, as the geographic view frames it; when the width runs
	// short, the places closest in longitude share a column.
	centre, arc := lonBounds(pts)
	for _, c := range places {
		c.key = math.Mod(c.lon-(centre-arc/2)+720+1e-6, 360)
	}
	sort.SliceStable(places, func(i, j int) bool { return places[i].key < places[j].key })
	// How many columns there are is settled with cards no wider than
	// vMaxW; only then may they widen to show every row in full. With the
	// map to itself, a card also says what runs on each node, and since
	// there is height to spare its cards may be wider and share columns
	// sooner.
	var runs map[string][]span
	if m.FullMap {
		runs = m.nodeServices()
	}
	natural, full := map[*vCard]int{}, map[*vCard]int{}
	for _, c := range cards {
		full[c] = c.natural(nodes, nil, false)
		natural[c] = min(full[c], vMaxW)
		if runs != nil {
			natural[c] = min(max(full[c], c.natural(nodes, runs, true)), vFullW)
		}
	}
	// The gap ordering does not depend on the candidate column count. Sort
	// once rather than multiplying O(n log n) sorting by every width retry.
	gaps := make([]int, len(places)-1)
	for i := range gaps {
		gaps[i] = i
	}
	sort.SliceStable(gaps, func(a, b int) bool {
		return places[gaps[a]+1].key-places[gaps[a]].key > places[gaps[b]+1].key-places[gaps[b]].key
	})
	split := func(k int) []int {
		cut := make([]bool, len(places))
		for _, i := range gaps[:k-1] {
			cut[i+1] = true
		}
		col := make([]int, len(places))
		for i := 1; i < len(places); i++ {
			col[i] = col[i-1]
			if cut[i] {
				col[i]++
			}
		}
		return col
	}
	nc := len(places)
	if maxCols > 0 {
		nc = min(nc, maxCols)
	}
	for ; nc > 1; nc-- {
		cw := make([]int, nc)
		for i, c := range split(nc) {
			cw[c] = max(cw[c], natural[places[i]])
		}
		need := 4 + (nc-1)*vMinGap
		for _, v := range cw {
			need += v
		}
		if need <= w {
			break
		}
	}
	for i, c := range split(nc) {
		places[i].col = c
	}
	// A card without a place goes to the column its neighbours average,
	// counting other unplaced cards at half weight.
	near := make([][]int, len(cards))
	for _, e := range edges {
		a, okA := idx[e.From]
		b, okB := idx[e.To]
		if okA && okB && cardOf[a] != cardOf[b] {
			near[cardOf[a]] = append(near[cardOf[a]], cardOf[b])
			near[cardOf[b]] = append(near[cardOf[b]], cardOf[a])
		}
	}
	colAt := make([]float64, len(cards))
	for k, c := range cards {
		colAt[k] = float64(c.col)
		if !c.located {
			colAt[k] = -1
		}
	}
	for it := 0; it < 6; it++ {
		for k, c := range cards {
			if c.located {
				continue
			}
			sum, n := 0.0, 0.0
			for _, o := range near[k] {
				switch {
				case cards[o].located:
					sum, n = sum+colAt[o], n+1
				case colAt[o] >= 0:
					sum, n = sum+colAt[o]/2, n+.5
				}
			}
			if n > 0 {
				colAt[k] = sum / n
			} else if it == 5 || colAt[k] < 0 {
				colAt[k] = float64(nc - 1)
			}
		}
	}
	for k, c := range cards {
		if !c.located {
			c.col = min(max(int(math.Round(colAt[k])), 0), nc-1)
		}
	}
	cols := make([][]*vCard, nc)
	for _, c := range cards {
		cols[c.col] = append(cols[c.col], c)
	}
	// Columns are as wide as their widest card; when that is too wide, the
	// widest give way first.
	colW := make([]int, nc)
	for _, c := range cards {
		colW[c.col] = max(colW[c.col], natural[c])
	}
	for {
		total, wide := 4+(nc-1)*vMinGap, 0
		for l, v := range colW {
			total += v
			if v > colW[wide] {
				wide = l
			}
		}
		if total <= w || colW[wide] <= vMinW {
			break
		}
		colW[wide]--
	}
	// What runs on a node goes on a line under it when every column has
	// the height for those lines with its cards two rows apart, and after
	// its name otherwise.
	lined := runs != nil
	for _, list := range cols {
		need := -2
		for _, c := range list {
			need += 3
			if !c.pill() {
				need += len(c.rows) + 1
				for _, i := range c.rows {
					if len(runs[nodes[i].ID]) > 0 {
						need++
					}
				}
			}
		}
		lined = lined && need <= h
	}
	rich := map[*vCard]int{}
	for _, c := range cards {
		rich[c] = c.natural(nodes, runs, lined)
		if lined && !c.pill() {
			c.svc = make([]bool, len(c.rows))
			for k, i := range c.rows {
				c.svc[k] = len(runs[nodes[i].ID]) > 0
			}
		}
	}
	// Room to spare goes first to cards whose names are cut short, then to
	// saying what runs on each node, a cell at a time to the column
	// missing most, while the gaps keep room for their wires.
	for phase, want := range []map[*vCard]int{full, rich} {
		wantW := make([]int, nc)
		for _, c := range cards {
			wantW[c.col] = max(wantW[c.col], want[c])
		}
		gap := vWideGap
		if phase == 1 {
			gap = vMinGap + 1
		}
		for {
			total, short := 4+(nc-1)*gap, -1
			for l, v := range colW {
				total += v
				if v < wantW[l] && (short < 0 || wantW[l]-v > wantW[short]-colW[short]) {
					short = l
				}
			}
			if short < 0 || total >= w {
				break
			}
			colW[short]++
		}
	}
	spare := w
	for _, v := range colW {
		spare -= v
	}
	// Outer margins leave two tracks for wires around the outer cards when
	// the gaps can spare them.
	margin := 2
	for margin < 4 && spare-2*(margin+1) >= (nc-1)*vMinGap {
		margin++
	}
	gap, extra := 0, 0
	if nc > 1 {
		gap = (spare - 2*margin) / (nc - 1)
		extra = spare - 2*margin - gap*(nc-1)
	} else {
		margin = max(2, spare/2)
	}
	colX := make([]int, nc)
	for l, cx := 0, margin; l < nc; l++ {
		colX[l] = cx
		cx += colW[l] + gap
		if l < extra {
			cx++
		}
	}
	for _, c := range cards {
		c.w = min(rich[c], colW[c.col])
		c.x = colX[c.col] + (colW[c.col]-c.w)/2
	}

	// Rows: cards stand two rows apart, room for two wires between them. A
	// column that is too tall gives up one of those rows, then folds its
	// longest cards a row at a time, then folds cards to chips, then gives
	// up the last row between them.
	gapOf := make([]int, nc)
	for l, list := range cols {
		gapOf[l] = 2
		for _, c := range list {
			c.show(len(c.rows), nodes, f)
		}
		for {
			need, long, big := -gapOf[l], (*vCard)(nil), (*vCard)(nil)
			for _, c := range list {
				need += c.h + gapOf[l]
				if c.h > 3 && (long == nil || c.h > long.h) {
					long = c
				}
				if c.h == 3 && (big == nil || len(c.rows) > len(big.rows)) {
					big = c
				}
			}
			switch {
			case need <= h:
			case gapOf[l] > 1:
				gapOf[l]--
				continue
			case long != nil:
				long.show(long.h-3, nodes, f)
				continue
			case big != nil:
				big.show(0, nodes, f)
				continue
			case gapOf[l] > 0:
				gapOf[l] = 0
				continue
			}
			break
		}
		// A chip is only as wide as its place and glyphs.
		for _, c := range list {
			if c.chip() {
				c.w = min(c.w, width(c.title())+min(len(c.rows), 5)+3)
				c.x = colX[l] + (colW[l]-c.w)/2
			}
		}
	}
	rowOf := make([]int, len(nodes))
	for _, c := range cards {
		r := 0
		for k, i := range c.rows {
			switch {
			case c.h == 1:
				rowOf[i] = 0
			case c.shown[k]:
				r++
				rowOf[i] = r
				if c.svc != nil && c.svc[k] {
					r++
				}
			default:
				rowOf[i] = c.h - 1
			}
		}
	}
	// Latitude orders each column and loosely sets the height: halfway
	// between even ranks and true proportion, so a far-south place neither
	// squeezes the rest together nor leaves the middle empty. A card without
	// a place moves toward the rows it links to.
	lats := []float64{}
	for _, c := range places {
		lats = append(lats, c.lat)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(lats)))
	for _, c := range cards {
		c.want = float64(h-c.h) / 2
		if c.located && lats[0] > lats[len(lats)-1] {
			rank := sort.Search(len(lats), func(i int) bool { return lats[i] <= c.lat })
			t := float64(rank)/float64(len(lats)-1)/2 + (lats[0]-c.lat)/(lats[0]-lats[len(lats)-1])/2
			c.want = t * float64(h-c.h)
		}
	}
	for it := 0; it < 6; it++ {
		for l, list := range cols {
			vPack(list, h, gapOf[l])
		}
		for k, c := range cards {
			if c.located {
				continue
			}
			sum, n := 0.0, 0.0
			for _, e := range edges {
				a, okA := idx[e.From]
				b, okB := idx[e.To]
				if !okA || !okB {
					continue
				}
				if cardOf[b] == k {
					a, b = b, a
				}
				if cardOf[a] == k && cardOf[b] != k {
					sum += float64(cards[cardOf[b]].y + rowOf[b] - rowOf[a])
					n++
				}
			}
			if n > 0 {
				c.want = sum / n
			}
		}
	}
	// Each card row is a port; the wires are routed longest first, so the
	// long ones run straight and the short ones find their way around them,
	// and the hops of declared paths come last.
	portOf := make([]int32, len(nodes))
	ports := map[[2]int]int32{}
	for i := range nodes {
		k := [2]int{cardOf[i], rowOf[i]}
		p, ok := ports[k]
		if !ok {
			p = int32(len(ports))
			ports[k] = p
		}
		portOf[i] = p
	}
	for k := range drawn {
		drawn[k].pa, drawn[k].pb = portOf[drawn[k].a], portOf[drawn[k].b]
	}
	reach := func(d vEdge) int {
		A, B := cards[d.ca], cards[d.cb]
		return abs(A.x+A.w/2-B.x-B.w/2) + abs(A.y+rowOf[d.a]-B.y-rowOf[d.b])
	}
	sort.SliceStable(drawn, func(i, j int) bool {
		if pi, pj := drawn[i].e.Kind == edgePath, drawn[j].e.Kind == edgePath; pi != pj {
			return pj
		}
		return reach(drawn[i]) > reach(drawn[j])
	})
	return &vLayout{w: w, h: h, cols: nc, cards: cards, cardOf: cardOf, rowOf: rowOf, portOf: portOf, drawn: drawn}
}

// virtualRows is how many rows the virtual map needs at width w to show
// every card unfolded, two rows apart, with a third more for latitude to
// show; 0 when no node has a location or the graph needs the bounded fallback.
func (m *Model) virtualRows(w int) int {
	nodes, edges, _ := m.graph()
	l := m.layoutVirtual(w, 1<<12, 0, nodes, edges, mapFocus{})
	if l == nil {
		return 0
	}
	need := make([]int, l.cols)
	for _, c := range l.cards {
		need[c.col] += c.h + 2
	}
	rows := 0
	for _, n := range need {
		rows = max(rows, n)
	}
	return rows + rows/3
}

func unrouted(paths [][]int) int {
	n := 0
	for _, p := range paths {
		if p == nil {
			n++
		}
	}
	return n
}

// ports lists the cells beside a node's row where its wires may leave or
// arrive: left and right of its card, inside the area's border columns.
func (l *vLayout) ports(node int) []int {
	c := l.cards[l.cardOf[node]]
	y := c.y + l.rowOf[node]
	out := []int{}
	if y < 0 || y >= l.h {
		return out
	}
	if c.x-1 >= 1 {
		out = append(out, y*l.w+c.x-1)
	}
	if c.x+c.w <= l.w-2 {
		out = append(out, y*l.w+c.x+c.w)
	}
	return out
}

// key identifies everything routing depends on: the area, where each card
// and port is, and which edges are drawn in which order.
func (l *vLayout) key() uint64 {
	f := fnv.New64a()
	var b [4]byte
	put := func(vs ...int) {
		for _, v := range vs {
			binary.LittleEndian.PutUint32(b[:], uint32(v))
			f.Write(b[:])
		}
	}
	put(l.w, l.h)
	for _, c := range l.cards {
		put(c.x, c.y, c.w, c.h)
	}
	for i := range l.rowOf {
		put(l.rowOf[i], l.cardOf[i])
	}
	for _, d := range l.drawn {
		put(d.a, d.b)
	}
	return f.Sum64()
}

// router sets up the grid: cards are solid, as are the area's two border
// columns; beside each card, the cells next to its rows are their ports and
// the rest of that column is closed; above and below a card costs a little.
func (l *vLayout) router() *vRouter {
	w, h := l.w, l.h
	r := newRouter(w, h)
	for y := 0; y < h; y++ {
		r.solid[y*w], r.solid[y*w+w-1] = true, true
	}
	ports := map[[2]int]int32{}
	for i, p := range l.portOf {
		ports[[2]int{l.cardOf[i], l.rowOf[i]}] = p
	}
	for k, c := range l.cards {
		for y := c.y - 1; y <= c.y+c.h; y++ {
			for x := c.x - 1; x <= c.x+c.w; x++ {
				if x < 0 || y < 0 || x >= w || y >= h {
					continue
				}
				i := y*w + x
				switch {
				case y == c.y-1 || y == c.y+c.h:
					if x >= c.x && x < c.x+c.w {
						r.near[i] = true
					}
				case x == c.x-1 || x == c.x+c.w:
					r.port[i] = -2
					if p, ok := ports[[2]int{k, y - c.y}]; ok {
						r.port[i], r.zone[i] = p, p
						if x > c.x && x+1 < w {
							r.zone[i+1] = p
						} else if x < c.x && x > 0 {
							r.zone[i-1] = p
						}
					}
				default:
					r.solid[i] = true
				}
			}
		}
	}
	return r
}

// routeBudget lays every drawn edge's wire, each around those laid before it.
// The same work counter is retained across edges, retries and alternate layouts.
// An unfinished search drops a wire; it never fabricates a partial route.
func (l *vLayout) routeBudget(work *layoutWork) [][]int {
	best := make([][]int, len(l.drawn))
	if work == nil || !mapLayoutAllowed(l.w, l.h, len(l.cardOf), len(l.drawn)) {
		return best
	}
	order := make([]int, len(l.drawn))
	for k := range order {
		order[k] = k
	}
	bestN := -1
	for try := 0; try < 4 && work.remaining > 0; try++ {
		r := l.router()
		r.work = work
		r.ends = make([][2]int32, len(l.drawn))
		for k, d := range l.drawn {
			r.ends[k] = [2]int32{d.pa, d.pb}
		}
		paths := make([][]int, len(l.drawn))
		n := 0
		for _, k := range order {
			if work.remaining == 0 {
				break
			}
			d := l.drawn[k]
			if paths[k] = r.path(l.ports(d.a), l.ports(d.b), k); paths[k] != nil {
				r.ws.route(corners(paths[k], l.w), k, 0, 0, 0)
				n++
			}
		}
		if n > bestN {
			best, bestN = paths, n
		}
		if n == len(l.drawn) {
			break
		}
		failed, rest := []int{}, []int{}
		for _, k := range order {
			if paths[k] == nil {
				failed = append(failed, k)
			} else {
				rest = append(rest, k)
			}
		}
		order = append(failed, rest...)
	}
	return best
}
