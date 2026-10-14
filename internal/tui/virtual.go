package tui

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"slices"
	"sort"
	"strconv"
)

// The virtual geo view keeps the world's order but not its distances. Each
// place is a card, and cards stand in columns from west to east along the
// shortest arc of longitude that holds them all, north above south, so a
// crowded city gets as much room as a lone island. Every node keeps a row of
// its own on its card and links run as wires between the rows, so one picture
// says both where things are and how they connect. Nodes without a location,
// such as edges and the internet, float as pills beside what they connect to.

const (
	vMaxW   = 24 // widest card
	vMinW   = 12 // narrowest card when space is short
	vMinGap = 5  // between columns: room for three wires between two cards
)

type vCard struct {
	rows     []int // node indices, parents before their children
	depth    []int
	shown    []bool // rows on the card; the rest are counted in its frame
	place    string
	lat, lon float64
	located  bool
	key      float64 // longitude along the arc, from its western end
	col      int
	x, y     int
	w, h     int
	want     float64 // wanted top row
	links    []vLink // links between two nodes on this card
}

// vLink is a link between two nodes on one card, drawn inside it.
type vLink struct {
	from, to int // node indices
	c        Color
	pri      int8
}

// vLane is a bracket inside a card's right edge joining the rows of the
// card's own links into one destination.
type vLane struct {
	to     int   // row of the destination
	from   []int // rows of the sources
	lo, hi int
	col    int // 0 is outermost
	c      Color
	pri    int8
}

// pill reports whether a card is drawn as a one-row pill: a single node
// without a location.
func (c *vCard) pill() bool { return !c.located && len(c.rows) == 1 }

// chip reports whether a card is folded to one row: its place and a glyph
// per node.
func (c *vCard) chip() bool { return c.h == 1 && !c.pill() }

func (c *vCard) title() string {
	switch {
	case !c.located:
		return "no location"
	case c.place != "":
		return c.place
	}
	return coords(c.lat, c.lon)
}

// natural is the width a card needs to show every row in full.
func (c *vCard) natural(nodes []mapNode) int {
	w := 0
	for k, i := range c.rows {
		w = max(w, 2*c.depth[k]+2+width(nodes[i].Name))
	}
	if c.pill() {
		return w + 2
	}
	if _, n := c.lanes(true); n > 0 {
		w += n + 2
	}
	return max(w, width(c.title())+2) + 4
}

// lanes lays the card's own links into brackets, one per destination row,
// side by side where they overlap and the longest outermost; links to or
// from a folded row are left out, and so are lanes beyond the third. all
// counts every row as shown.
func (c *vCard) lanes(all bool) ([]vLane, int) {
	row := map[int]int{}
	r := 0
	for k, i := range c.rows {
		if all || c.shown[k] {
			r++
			row[i] = r
		}
	}
	lanes := []vLane{}
	for _, l := range c.links {
		a, okA := row[l.from]
		b, okB := row[l.to]
		if !okA || !okB || a == b {
			continue
		}
		k := 0
		for k < len(lanes) && lanes[k].to != b {
			k++
		}
		if k == len(lanes) {
			lanes = append(lanes, vLane{to: b, lo: b, hi: b, c: l.c, pri: l.pri})
		}
		ln := &lanes[k]
		ln.from = append(ln.from, a)
		ln.lo, ln.hi = min(ln.lo, a), max(ln.hi, a)
		if l.pri > ln.pri {
			ln.c, ln.pri = l.c, l.pri
		}
	}
	sort.SliceStable(lanes, func(i, j int) bool { return lanes[i].hi-lanes[i].lo > lanes[j].hi-lanes[j].lo })
	out, n := []vLane{}, 0
	for _, ln := range lanes {
		for ln.col = 0; ln.col < 3; ln.col++ {
			free := true
			for _, o := range out {
				if o.col == ln.col && ln.lo <= o.hi && o.lo <= ln.hi {
					free = false
				}
			}
			if free {
				break
			}
		}
		if ln.col < 3 {
			out = append(out, ln)
			n = max(n, ln.col+1)
		}
	}
	return out, n
}

// show keeps n rows on the card: the focused node first, then nodes with a
// problem, then top-level nodes, each group in order. The card's height
// follows from n alone, so moving the selection never moves a card. With n
// at 0 the card folds to a one-row chip.
func (c *vCard) show(n int, nodes []mapNode, f mapFocus) {
	if n <= 0 && !c.pill() {
		c.shown, c.h = make([]bool, len(c.rows)), 1
		return
	}
	n = min(max(n, 1), len(c.rows))
	rank := func(k int) int {
		switch nd := nodes[c.rows[k]]; {
		case f.hasNode(nd.ID):
			return 0
		case signalRank(nodeSignal(nd)) >= 3:
			return 1
		case c.depth[k] == 0:
			return 2
		}
		return 3
	}
	order := make([]int, len(c.rows))
	for k := range order {
		order[k] = k
	}
	sort.SliceStable(order, func(a, b int) bool { return rank(order[a]) < rank(order[b]) })
	c.shown = make([]bool, len(c.rows))
	for _, k := range order[:n] {
		c.shown[k] = true
	}
	c.h = 1
	if !c.pill() {
		c.h = n + 2
	}
}

func (c *vCard) hidden() int {
	n := 0
	for _, s := range c.shown {
		if !s {
			n++
		}
	}
	return n
}

// coords is a position to the nearest degree, e.g. "34°N 118°W".
func coords(lat, lon float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		ns = "S"
	}
	lon = math.Mod(math.Mod(lon, 360)+540, 360) - 180
	if lon < 0 {
		ew = "W"
	}
	return strconv.Itoa(int(math.Round(math.Abs(lat)))) + "°" + ns + " " + strconv.Itoa(int(math.Round(math.Abs(lon)))) + "°" + ew
}

// lonDiff is b-a in degrees, between -180 and 180.
func lonDiff(a, b float64) float64 {
	return math.Mod(math.Mod(b-a, 360)+540, 360) - 180
}

// virtualCards groups located nodes into places, by declared place within a
// few degrees or by position within half a degree, and gives each node
// without a location a card of its own shared with its unlocated children.
func virtualCards(nodes []mapNode) []*vCard {
	idx := map[string]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}
	cards := []*vCard{}
	anchor := map[*vCard][2]float64{}
	byRoot := map[string]*vCard{}
	for i, n := range nodes {
		if n.Located {
			var c *vCard
			for _, o := range cards {
				a, ok := anchor[o]
				if !ok {
					continue
				}
				dLat, dLon := math.Abs(a[0]-n.Lat), math.Abs(lonDiff(a[1], n.Lon))
				if n.Place != "" && n.Place == o.place && dLat < 3 && dLon < 3 || dLat < .5 && dLon < .5 {
					c = o
					break
				}
			}
			if c == nil {
				c = &vCard{place: n.Place, located: true}
				anchor[c] = [2]float64{n.Lat, n.Lon}
				cards = append(cards, c)
			}
			if c.place == "" {
				c.place = n.Place
			}
			c.rows = append(c.rows, i)
			continue
		}
		root := n.ID
		for k, p := 0, n.Parent; k < 16 && p != ""; k++ {
			j, ok := idx[p]
			if !ok || nodes[j].Located {
				break
			}
			root, p = p, nodes[j].Parent
		}
		c := byRoot[root]
		if c == nil {
			c = &vCard{}
			byRoot[root] = c
			cards = append(cards, c)
		}
		c.rows = append(c.rows, i)
	}
	for _, c := range cards {
		if a, ok := anchor[c]; ok {
			dLat, dLon := 0.0, 0.0
			for _, i := range c.rows {
				dLat += nodes[i].Lat - a[0]
				dLon += lonDiff(a[1], nodes[i].Lon)
			}
			c.lat, c.lon = a[0]+dLat/float64(len(c.rows)), a[1]+dLon/float64(len(c.rows))
		}
		c.nest(nodes)
	}
	return cards
}

// nest orders a card's rows so each parent on the card comes before its
// children, which are indented under it.
func (c *vCard) nest(nodes []mapNode) {
	on := map[string]bool{}
	for _, i := range c.rows {
		on[nodes[i].ID] = true
	}
	kids := map[string][]int{}
	roots := []int{}
	for _, i := range c.rows {
		if p := nodes[i].Parent; p != "" && p != nodes[i].ID && on[p] {
			kids[p] = append(kids[p], i)
		} else {
			roots = append(roots, i)
		}
	}
	rows, depth := []int{}, []int{}
	done := map[int]bool{}
	var walk func(i, d int)
	walk = func(i, d int) {
		if done[i] {
			return
		}
		done[i] = true
		rows, depth = append(rows, i), append(depth, d)
		for _, k := range kids[nodes[i].ID] {
			walk(k, min(d+1, 4))
		}
	}
	for _, i := range roots {
		walk(i, 0)
	}
	// Nodes caught in a parent cycle have no root; they stand on their own.
	for _, i := range c.rows {
		walk(i, 0)
	}
	c.rows, c.depth = rows, depth
}

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
	centre, span := lonBounds(pts)
	for _, c := range places {
		c.key = math.Mod(c.lon-(centre-span/2)+720+1e-6, 360)
	}
	sort.SliceStable(places, func(i, j int) bool { return places[i].key < places[j].key })
	natural := map[*vCard]int{}
	for _, c := range cards {
		natural[c] = min(c.natural(nodes), vMaxW)
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
		c.w = min(natural[c], colW[c.col])
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

func (m *Model) drawVirtual(cv *Canvas, x, y, w, h int, nodes []mapNode, edges []mapEdge, groups map[string]Color, f mapFocus) {
	if !mapLayoutAllowed(w, h, len(nodes), len(edges)) {
		drawLayoutLimit(cv, x, y, w, h, len(nodes), len(edges))
		return
	}
	if len(nodes) == 0 {
		m.drawNetwork(cv, x, y, w, h, nodes, edges, groups, f)
		return
	}
	l := m.layoutVirtual(w, h, 0, nodes, edges, f)
	if l == nil {
		msg := "No node locations declared — add one with e (edit), or press m to change the map"
		cv.Text(x+max(0, (w-width(msg))/2), y+h/2, msg, fg(cMuted).italic(), w)
		return
	}
	// Routing is the costly part and between frames only the animation
	// moves, so the wires are kept while the layout holds. When some wires
	// find no way through, one column fewer leaves wider gaps between the
	// columns; the layout that lays more wires wins, within the same budget.
	key := l.key()
	if m.vcache.key != key {
		work := newLayoutWork()
		c := vCache{key: key, paths: l.routeBudget(work)}
		c.dropped = unrouted(c.paths)
		if c.dropped > 0 && l.cols > 1 && work.remaining > 0 {
			if l2 := m.layoutVirtual(w, h, l.cols-1, nodes, edges, f); l2 != nil {
				if p2 := l2.routeBudget(work); unrouted(p2) < c.dropped {
					c.cols, c.paths, c.dropped = l.cols-1, p2, unrouted(p2)
				}
			}
		}
		m.vcache = c
	}
	if m.vcache.cols > 0 {
		l = m.layoutVirtual(w, h, m.vcache.cols, nodes, edges, f)
	}
	// Each end of a wire meets its card's edge on one side of one row.
	type end struct {
		card, row int
		right     bool
	}
	ends := map[end][]int{} // the edges ending there; arrivals as -k-1
	ws := newWires(w, h)
	cellsOf := make([][][2]int, len(l.drawn))
	for k, d := range l.drawn {
		path := m.vcache.paths[k]
		if path == nil {
			continue
		}
		c, pri, dash := edgeStyle(d.e, f)
		cellsOf[k] = ws.route(corners(path, w), k, c, pri, dash)
		a, b := path[0], path[len(path)-1]
		ea := end{d.ca, l.rowOf[d.a], a%w > l.cards[d.ca].x}
		eb := end{d.cb, l.rowOf[d.b], b%w > l.cards[d.cb].x}
		ends[ea] = append(ends[ea], k)
		if d.e.Kind == edgeContain {
			ends[eb] = append(ends[eb], k)
		} else {
			ends[eb] = append(ends[eb], -k-1)
		}
	}
	ws.draw(cv, x, y)
	phase := m.phase()
	for k, cells := range cellsOf {
		if e := l.drawn[k].e; len(cells) >= 6 && (e.Signal == "up" || e.Signal == "carried") && e.Kind != edgeContain {
			ws.glint(cv, x, y, cells, k, e.ID, phase)
		}
	}
	for _, c := range l.cards {
		m.drawVCard(cv, x, y, h, c, nodes, groups, f)
	}
	// Where wires meet a card's frame: an arrowhead where one arrives, a tee
	// where they only leave, in the color of the one that needs the most
	// attention. A pill or chip has no frame, so its arrowhead stands just
	// outside it.
	keys := make([]end, 0, len(ends))
	for k := range ends {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.card != b.card {
			return a.card < b.card
		}
		if a.row != b.row {
			return a.row < b.row
		}
		return !a.right && b.right
	})
	for _, k := range keys {
		top, in := -1, false
		var c Color
		var best int8
		for _, e := range ends[k] {
			if e < 0 {
				in, e = true, -e-1
			}
			if ec, pri, _ := edgeStyle(l.drawn[e].e, f); top < 0 || pri > best {
				top, best, c = e, pri, ec
			}
		}
		c = Mix(c, cGlow, .2) // a touch brighter than the wire, so ends read
		card := l.cards[k.card]
		bx, by := card.x, card.y+k.row
		if k.right {
			bx = card.x + card.w - 1
		}
		if by < 0 || by >= h {
			continue
		}
		if card.h == 1 {
			if in {
				ax, r := bx-1, '▸'
				if k.right {
					ax, r = bx+1, '◂'
				}
				cv.Set(x+ax, y+by, r, fg(c))
			}
			continue
		}
		g := '┤'
		switch {
		case in && k.right:
			g = '◂'
		case in:
			g = '▸'
		case k.row == card.h-1:
			g = '┴' // the folded rows, at a corner of the frame
		case k.right:
			g = '├'
		}
		cv.Set(x+bx, y+by, g, fg(c).Bg(cv.at(x+bx, y+by).st.BG))
	}
	tag := " virtual geo "
	if tx := x + w - width(tag) - 1; tx > x {
		for i := tx; i < tx+width(tag); i++ {
			if cl := cv.at(i, y); cl.ch != " " || cl.st.FG != 0 {
				return
			}
		}
		cv.Text(tx, y, tag, fg(cFaint), -1)
	}
}

// drawVCard draws a place as a card: its name in the frame, one row per node
// with children indented under their parents, and its position (or how many
// rows are folded away) along the foot. A node without a place is a pill.
func (m *Model) drawVCard(cv *Canvas, ox, oy, h int, c *vCard, nodes []mapNode, groups map[string]Color, f mapFocus) {
	in := func(yy int) bool { return yy >= 0 && yy < h }
	paper := Mix(cInk, cKeyBG, .38)
	signal, focused := "", false
	for _, i := range c.rows {
		signal = worseSignal(signal, nodeSignal(nodes[i]))
		focused = focused || f.hasNode(nodes[i].ID)
	}
	if c.pill() {
		i := c.rows[0]
		n := nodes[i]
		if !in(c.y) {
			return
		}
		hue := providerColor(n.Provider, groups[n.Group])
		chip := Mix(hue, cInk, .84)
		st := fg(Mix(hue, cText, .35)).Bg(chip)
		if n.Kind == "external" {
			st = st.italic()
		}
		if focused {
			chip = cSelBG
			st = fg(cGlow).Bg(chip).bold()
		}
		sig := nodeSignal(n)
		cv.Set(ox+c.x, oy+c.y, ' ', Style{BG: chip})
		cv.Set(ox+c.x+1, oy+c.y, signalGlyph(sig), fg(signalColor(sig)).Bg(chip).bold())
		cv.Pad(ox+c.x+2, oy+c.y, " "+ellipsis(n.Name, c.w-4)+" ", st, c.w-2)
		m.hit(ox+c.x, oy+c.y, c.w, "select:node:"+n.ID)
		return
	}
	if c.chip() {
		if !in(c.y) {
			return
		}
		lead := c.rows[0]
		bg, st := paper, fg(cText).Bg(paper).bold()
		if focused {
			bg, st = cSelBG, fg(cGlow).Bg(cSelBG).bold()
			for _, i := range c.rows {
				if f.hasNode(nodes[i].ID) {
					lead = i
				}
			}
		}
		cv.Fill(ox+c.x, oy+c.y, c.w, 1, Style{BG: bg})
		glyphs := min(len(c.rows), 5)
		n := cv.Text(ox+c.x+1, oy+c.y, ellipsis(c.title(), c.w-3-glyphs), st, c.w-3-glyphs)
		for k, i := range c.rows[:glyphs] {
			sig := nodeSignal(nodes[i])
			cv.Set(ox+c.x+2+n+k, oy+c.y, signalGlyph(sig), fg(signalColor(sig)).Bg(bg))
		}
		m.hit(ox+c.x, oy+c.y, c.w, "select:node:"+nodes[lead].ID)
		return
	}
	frame := fg(Mix(cLine, cMuted, .3))
	switch {
	case focused:
		frame = fg(Mix(cLine, cAccent, .7))
	case signal == "down":
		frame = fg(Mix(cLine, cDown, .55))
	case signalRank(signal) >= 3:
		frame = fg(Mix(cLine, cWarn, .45))
	}
	corners := [6]rune{'╭', '╮', '╰', '╯', '─', '│'}
	switch {
	case !c.located:
		corners = [6]rune{'╭', '╮', '╰', '╯', '┄', '┆'}
	case focused && m.NoColor:
		corners = [6]rune{'┏', '┓', '┗', '┛', '━', '┃'}
	}
	frame = frame.Bg(paper)
	bottom := c.y + c.h - 1
	for yy := c.y; yy <= bottom; yy++ {
		if !in(yy) {
			continue
		}
		l, r, mid := corners[5], corners[5], ' '
		switch yy {
		case c.y:
			l, r, mid = corners[0], corners[1], corners[4]
		case bottom:
			l, r, mid = corners[2], corners[3], corners[4]
		}
		cv.Set(ox+c.x, oy+yy, l, frame)
		for xx := c.x + 1; xx < c.x+c.w-1; xx++ {
			cv.Set(ox+xx, oy+yy, mid, frame)
		}
		cv.Set(ox+c.x+c.w-1, oy+yy, r, frame)
	}
	lead := c.rows[0]
	if in(c.y) {
		ts := fg(cText).Bg(paper).bold()
		if !c.located {
			ts = fg(cMuted).Bg(paper).italic()
		}
		n := cv.Text(ox+c.x+2, oy+c.y, " "+ellipsis(c.title(), c.w-6)+" ", ts, c.w-4)
		m.hit(ox+c.x+2, oy+c.y, n, "select:node:"+nodes[lead].ID)
	}
	if in(bottom) {
		foot, fs := "", fg(cFaint).Bg(paper)
		if n := c.hidden(); n > 0 {
			foot, fs = "+"+itoa(n)+" more", fg(cMuted).Bg(paper)
		} else if c.located {
			foot = coords(c.lat, c.lon)
		}
		if foot != "" && width(foot)+4 <= c.w-2 {
			cv.Text(ox+c.x+c.w-width(foot)-4, oy+bottom, " "+foot+" ", fs, -1)
		}
	}
	lanes, nl := c.lanes(false)
	laneW := 0
	if nl > 0 {
		laneW = nl + 2
	}
	row := 0
	for k, i := range c.rows {
		if !c.shown[k] {
			continue
		}
		row++
		yy := c.y + row
		if !in(yy) {
			continue
		}
		n := nodes[i]
		bg := paper
		st := fg(Mix(groups[n.Group], cText, .35)).Bg(bg)
		if n.Kind == "external" {
			st = st.italic()
		}
		if f.hasNode(n.ID) {
			bg = cSelBG
			st = fg(cGlow).Bg(bg).bold()
		}
		inner := c.w - 2
		cv.Fill(ox+c.x+1, oy+yy, inner, 1, Style{BG: bg})
		if f.hasNode(n.ID) && m.NoColor {
			cv.Set(ox+c.x+1, oy+yy, '›', st)
		}
		cx := ox + c.x + 2
		if d := c.depth[k]; d > 0 {
			guide := '└'
			for j := k + 1; j < len(c.rows) && c.depth[j] >= d; j++ {
				if c.depth[j] == d && c.shown[j] {
					guide = '├'
					break
				}
			}
			cx += 2 * (d - 1)
			cv.Set(cx, oy+yy, guide, fg(cFaint).Bg(bg))
			cx += 2
		}
		sig := nodeSignal(n)
		cv.Set(cx, oy+yy, signalGlyph(sig), fg(signalColor(sig)).Bg(bg).bold())
		cx += 2
		if room := ox + c.x + c.w - 2 - laneW - cx; room > 0 {
			cv.Text(cx, oy+yy, ellipsis(n.Name, room), st, room)
		}
		m.hit(ox+c.x, oy+yy, c.w, "select:node:"+n.ID)
	}
	// The card's own links: each source row runs right into its lane, which
	// carries it to an arrowhead at the destination row.
	kx := c.x + c.w - 3 - nl
	for pass := 0; pass < 2; pass++ {
		for _, ln := range lanes {
			lx := c.x + c.w - 3 - ln.col
			if pass == 0 {
				for r := ln.lo; r <= ln.hi; r++ {
					var dirs uint8
					if r > ln.lo {
						dirs |= wN
					}
					if r < ln.hi {
						dirs |= wS
					}
					if r == ln.to || slices.Contains(ln.from, r) {
						dirs |= wW
					}
					if in(c.y + r) {
						cv.Set(ox+lx, oy+c.y+r, wireGlyph[dirs], fg(ln.c))
					}
				}
				continue
			}
			// Second pass: the runs into the lanes, over any lane they cross.
			for _, r := range append(append([]int{}, ln.from...), ln.to) {
				if !in(c.y + r) {
					continue
				}
				for xx := kx; xx < lx; xx++ {
					g := '─'
					if xx == kx && r == ln.to {
						g = '◂'
					}
					if xx == kx && r != ln.to && cv.at(ox+xx, oy+c.y+r).ch == "◂" {
						continue
					}
					cv.Set(ox+xx, oy+c.y+r, g, fg(ln.c))
				}
			}
		}
	}
}
