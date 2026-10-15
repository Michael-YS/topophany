package tui

import (
	"math"
	"sort"
	"strconv"
)

const (
	vMaxW    = 24 // widest card a column count allows for
	vFullW   = 36 // the same with the map to itself, when cards say what runs on each node
	vMinW    = 12 // narrowest card when space is short
	vMinGap  = 5  // between columns: room for three wires between two cards
	vWideGap = 8  // the gap a card leaves when it widens to show its rows in full
)

type vCard struct {
	rows     []int // node indices, parents before their children
	depth    []int
	shown    []bool // rows on the card; the rest are counted in its frame
	svc      []bool // rows with a line under them saying what runs there
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

// natural is the width a card needs to show every row in full and, with
// runs, what runs on each node: after its name, or on a line of its own
// under it when lined.
func (c *vCard) natural(nodes []mapNode, runs map[string][]span, lined bool) int {
	w := 0
	for k, i := range c.rows {
		name, more := 2*c.depth[k]+2+width(nodes[i].Name), runsWidth(runs[nodes[i].ID])
		if lined && !c.pill() {
			w = max(w, name, 2*c.depth[k]+2+more)
		} else {
			w = max(w, name+more)
		}
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
			if !all && c.svc != nil && c.svc[k] {
				r++
			}
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
		for k := range c.rows {
			if c.shown[k] && c.svc != nil && c.svc[k] {
				c.h++
			}
		}
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
