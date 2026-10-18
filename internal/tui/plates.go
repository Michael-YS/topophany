package tui

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
)

// The geographic map's labels. Sites crowd where machines are and leave the
// oceans empty, so a site is named by a plate that goes where there is room:
// its place over a row for each node, set down where it covers the least land
// and the fewest arcs, beside its marker when it can be and joined to it by a
// thin leader when it cannot. A plate that finds no room shows fewer rows,
// then folds to one line, then leaves the marker alone. Sites are placed in
// an order the selection does not change, so labels stay put while it moves.

type plate struct {
	site       *geoSite
	k          int // the site's index
	title      string
	rows       []int // node indices on the plate, parents before children
	depth      []int
	more       int  // nodes the plate has no row for
	line       bool // folded to one line: the title and a glyph per node
	location   bool // one quiet place label, without node or service details
	x, y, w, h int  // cells within the map
	beside     bool // next to its marker, so no leader is drawn
}

// siteTitle names a site by its places, or by where it is when its nodes
// name no place.
func siteTitle(s *geoSite, nodes []mapNode) string {
	switch {
	case len(s.places) == 1:
		return s.places[0]
	case len(s.places) == 2:
		return s.places[0] + " · " + s.places[1]
	case len(s.places) > 2:
		return s.places[0] + " +" + itoa(len(s.places)-1)
	}
	n := nodes[s.nodes[0]]
	return coords(n.Lat, n.Lon)
}

// plateShapes are the shapes a site's label may take, the richest first:
// a plate with a row per node (no more than most), one with up to three, and
// one line. Rows a plate cannot hold go to the selection first, then to
// nodes with a problem, then to top-level nodes. With runs, a row goes on to
// say what runs on its node. No plate is wider inside than maxW.
func plateShapes(s *geoSite, nodes []mapNode, f mapFocus, most, maxW int, runs map[string][]span, locations bool) []plate {
	title := ellipsis(siteTitle(s, nodes), maxW)
	if locations {
		return []plate{{site: s, title: title, location: true, w: width(title) + 2, h: 1, k: -1}}
	}
	c := &vCard{rows: append([]int(nil), s.nodes...), located: true}
	c.nest(nodes)
	caps := []int{min(len(c.rows), most)}
	if caps[0] > 3 {
		caps = append(caps, 3)
	}
	shapes := []plate{}
	for _, n := range caps {
		if n < len(c.rows) {
			n-- // the last row says how many more
		}
		c.show(n, nodes, f)
		p := plate{site: s, title: title, more: c.hidden(), k: -1}
		// As wide as any of its nodes needs, whichever it has rows for, so
		// moving the selection never resizes a plate.
		inner := width(title)
		for k, i := range c.rows {
			inner = max(inner, 2*c.depth[k]+2+width(nodes[i].Name)+runsWidth(runs[nodes[i].ID]))
			if c.shown[k] {
				p.rows = append(p.rows, i)
				p.depth = append(p.depth, c.depth[k])
			}
		}
		if p.more > 0 {
			inner = max(inner, width("+"+itoa(p.more)+" more"))
		}
		p.w, p.h = min(inner, maxW)+3, 1+len(p.rows)
		if p.more > 0 {
			p.h++
		}
		shapes = append(shapes, p)
	}
	line := plate{site: s, title: title, line: true, h: 1, k: -1}
	line.w = width(title)
	if n := len(s.nodes); n > 1 {
		line.w += 1 + min(n, 5)
		if n > 5 {
			line.w += width("+" + itoa(n-5))
		}
	}
	return append(shapes, line)
}

// sat is a summed-area table over the map's cells, so the sum over any
// rectangle is four lookups.
type sat struct {
	w   int
	sum []int32
}

func newSat(w, h int, at func(x, y int) int32) sat {
	t := sat{w: w + 1, sum: make([]int32, (w+1)*(h+1))}
	for y := 0; y < h; y++ {
		row := int32(0)
		for x := 0; x < w; x++ {
			row += at(x, y)
			t.sum[(y+1)*t.w+x+1] = t.sum[y*t.w+x+1] + row
		}
	}
	return t
}

// over sums the cells x0 ≤ x < x1, y0 ≤ y < y1, clipped to the map.
func (t sat) over(x0, y0, x1, y1 int) int32 {
	h := len(t.sum)/t.w - 1
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, t.w-1), min(y1, h)
	if x0 >= x1 || y0 >= y1 {
		return 0
	}
	return t.sum[y1*t.w+x1] - t.sum[y0*t.w+x1] - t.sum[y1*t.w+x0] + t.sum[y0*t.w+x0]
}

// placePlates finds each site's label a place in the w×h map. land counts
// the land dots in each cell (0 to 8); arcs marks the cells the arcs pass
// through; taken marks cells no label may cover, such as the zoom tag.
// aspect is a cell's height over its width. Labels are placed one at a time,
// each where it costs least around those placed before it; since the order
// matters, a few orders are tried and the one that costs least in all wins.
func placePlates(sites []*geoSite, nodes []mapNode, f mapFocus, w, h int, aspect float64, land []uint8, arcs, taken []bool, runs map[string][]span, locations bool) ([]plate, bool) {
	return placePlatesBudget(sites, nodes, f, w, h, aspect, land, arcs, taken, runs, locations, newLayoutWork())
}

func placePlatesBudget(sites []*geoSite, nodes []mapNode, f mapFocus, w, h int, aspect float64, land []uint8, arcs, taken []bool, runs map[string][]span, locations bool, work *layoutWork) ([]plate, bool) {
	if !mapLayoutAllowed(w, h, len(nodes), 0) || len(sites) > maxLayoutNodes {
		return nil, false
	}
	if w <= 0 || h <= 0 || len(sites) == 0 {
		return nil, true
	}
	shapes := make([][]plate, len(sites))
	most, maxW := min(max(h/4, 3), 12), min(max(w/3, 18), 36)
	if runs != nil {
		maxW = min(max(w/3, 18), 48)
	}
	for k, s := range sites {
		shapes[k] = plateShapes(s, nodes, f, most, maxW, runs, locations)
		for i := range shapes[k] {
			shapes[k][i].k = k
		}
	}
	if !work.takeN(2 * w * h) {
		return nil, false
	}
	landT := newSat(w, h, func(x, y int) int32 { return int32(land[y*w+x]) })
	arcT := newSat(w, h, func(x, y int) int32 {
		if arcs[y*w+x] {
			return 1
		}
		return 0
	})
	idx := make([]int, len(sites))
	for k := range idx {
		idx[k] = k
	}
	orders := [][]int{}
	by := func(less func(a, b *geoSite) bool) {
		o := append([]int(nil), idx...)
		sort.SliceStable(o, func(i, j int) bool { return less(sites[o[i]], sites[o[j]]) })
		orders = append(orders, o)
	}
	by(func(a, b *geoSite) bool { // what needs attention first, then the biggest
		if ra, rb := signalRank(a.signal), signalRank(b.signal); ra != rb {
			return ra > rb
		}
		return len(a.nodes) > len(b.nodes)
	})
	by(func(a, b *geoSite) bool { return len(a.nodes) < len(b.nodes) }) // the smallest first
	by(func(a, b *geoSite) bool { return a.x < b.x })                   // west to east
	by(func(a, b *geoSite) bool { return a.x > b.x })                   // east to west
	var best []plate
	bestCost := math.Inf(1)
	for _, o := range orders {
		ps, cost, complete := placeInOrder(sites, shapes, o, w, h, aspect, landT, arcT, taken, work)
		if !complete {
			return nil, false
		}
		if cost < bestCost-1e-9 {
			best, bestCost = ps, cost
		}
	}
	return best, true
}

// plateCache keeps the geographic map's labels between frames: between
// two frames usually only the animation moves.
type plateCache struct {
	key      uint64
	plates   []plate
	complete bool
}

// plates are placePlates' labels, from the cache while nothing they depend
// on has changed.
func (m *Model) plates(sites []*geoSite, nodes []mapNode, f mapFocus, w, h int, aspect float64, land []uint8, arcs, taken []bool, runs map[string][]span) ([]plate, bool) {
	hs := fnv.New64a()
	var b [8]byte
	num := func(vs ...float64) {
		for _, v := range vs {
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
			hs.Write(b[:])
		}
	}
	str := func(ss ...string) {
		for _, s := range ss {
			hs.Write([]byte(s))
			hs.Write([]byte{0})
		}
	}
	num(float64(w), float64(h), aspect, float64(m.GeoLabels))
	for _, s := range sites {
		num(s.x, s.y, float64(len(s.nodes)))
		str(s.signal)
		for _, i := range s.nodes {
			str(nodes[i].ID, nodes[i].Name, nodes[i].Place, nodeSignal(nodes[i]), nodes[i].Parent)
			for _, r := range runs[nodes[i].ID] {
				str(r.s)
			}
			if f.hasNode(nodes[i].ID) {
				str("*")
			}
		}
	}
	for _, g := range [][]bool{arcs, taken} {
		for i, v := range g {
			if v {
				num(float64(i))
			}
		}
		str("|")
	}
	for _, v := range land {
		hs.Write([]byte{v})
	}
	key := hs.Sum64()
	if m.pcache.key != key || m.pcache.plates == nil {
		ps, complete := placePlates(sites, nodes, f, w, h, aspect, land, arcs, taken, runs, m.GeoLabels == geoLocations)
		m.pcache = plateCache{key: key, plates: ps, complete: complete}
		if m.pcache.plates == nil {
			m.pcache.plates = []plate{}
		}
	}
	out := append([]plate(nil), m.pcache.plates...)
	for i := range out {
		out[i].site = sites[out[i].k]
	}
	return out, m.pcache.complete
}

// placeInOrder places the labels in order and says what they cost in all:
// how far each is from its marker and what it covers, plus a toll for each
// label that had to shrink and a bigger one for each that found no room.
func placeInOrder(sites []*geoSite, shapes [][]plate, order []int, w, h int, aspect float64, landT, arcT sat, taken []bool, work *layoutWork) ([]plate, float64, bool) {
	if !work.takeN(2*w*h + len(sites)) {
		return nil, 0, false
	}
	cell := func(s *geoSite) (int, int) { return int(s.x) / 2, int(s.y) / 4 }
	occ := append([]bool(nil), taken...)
	soft := make([]bool, w*h) // the leaders laid so far
	for _, s := range sites {
		if cx, cy := cell(s); cx >= 0 && cy >= 0 && cx < w && cy < h {
			occ[cy*w+cx] = true
		}
	}
	bit := func(g []bool) func(x, y int) int32 {
		return func(x, y int) int32 {
			if g[y*w+x] {
				return 1
			}
			return 0
		}
	}
	reach := math.Max(16, math.Min(float64(w), float64(h)*aspect)/3)
	out, total := []plate{}, 0.0
	for _, k := range order {
		s := sites[k]
		mx, my := cell(s)
		if mx < 0 || my < 0 || mx >= w || my >= h {
			continue
		}
		if !work.takeN(2 * w * h) {
			return nil, 0, false
		}
		occT, softT := newSat(w, h, bit(occ)), newSat(w, h, bit(soft))
		// A leader should not run through another marker or label.
		crosses := func(x0, y0, x1, y1 int) int {
			n := 0
			steps := max(abs(x1-x0), abs(y1-y0))
			if !work.takeN(max(0, steps-1)) {
				return -1
			}
			for i := 1; i < steps; i++ {
				x := x0 + (x1-x0)*i/steps
				y := y0 + (y1-y0)*i/steps
				if occ[y*w+x] {
					n++
				}
			}
			return n
		}
		placed := false
		for level, p := range shapes[k] {
			found, cheapest := plate{}, math.Inf(1)
			ry := int(reach/aspect) + 1
			for py := max(0, my-p.h-ry); py <= min(h-p.h, my+ry); py++ {
				for px := max(0, mx-p.w-int(reach)); px <= min(w-p.w, mx+int(reach)); px++ {
					if !work.take() {
						return nil, 0, false
					}
					// A cell clear all round, so labels never touch.
					if occT.over(px-1, py-1, px+p.w+1, py+p.h+1) > 0 {
						continue
					}
					dx, dy := 0, 0
					switch {
					case mx < px:
						dx = px - mx
					case mx >= px+p.w:
						dx = mx - (px + p.w - 1)
					}
					switch {
					case my < py:
						dy = py - my
					case my >= py+p.h:
						dy = my - (py + p.h - 1)
					}
					dist := math.Hypot(float64(dx), float64(dy)*aspect)
					if dist > reach {
						continue
					}
					beside := dy == 0 && dx == 2 && my == py
					cost := dist + .04*float64(landT.over(px, py, px+p.w, py+p.h)) +
						.5*float64(arcT.over(px, py, px+p.w, py+p.h)) + 2*float64(softT.over(px, py, px+p.w, py+p.h))
					if px+p.w <= mx {
						cost += .5 // the right of a marker reads first
					}
					if !beside {
						cost += 6 // a leader costs ink
					}
					if cost >= cheapest {
						continue
					}
					if !beside {
						ax, ay := min(max(mx, px), px+p.w-1), min(max(my, py), py+p.h-1)
						crossed := crosses(mx, my, ax, ay)
						if crossed < 0 {
							return nil, 0, false
						}
						if cost += 3 * float64(crossed); cost >= cheapest {
							continue
						}
					}
					found, cheapest = p, cost
					found.x, found.y, found.beside = px, py, beside
				}
			}
			if math.IsInf(cheapest, 1) {
				continue
			}
			out = append(out, found)
			total += cheapest + 12*float64(level)
			if !work.takeN(found.w*found.h + max(w, h)) {
				return nil, 0, false
			}
			for y := found.y; y < found.y+found.h; y++ {
				for x := found.x; x < found.x+found.w; x++ {
					occ[y*w+x] = true
				}
			}
			if !found.beside {
				ax, ay := min(max(mx, found.x), found.x+found.w-1), min(max(my, found.y), found.y+found.h-1)
				steps := max(abs(ax-mx), abs(ay-my))
				for i := 1; i < steps; i++ {
					soft[(my+(ay-my)*i/steps)*w+mx+(ax-mx)*i/steps] = true
				}
			}
			placed = true
			break
		}
		if !placed {
			total += 60
		}
	}
	return out, total, true
}

// leader joins a plate to its marker with a line of dots from the marker's
// halo to the plate's nearest edge.
func (p plate) leader(d *Dots, c Color, ky float64) {
	s := p.site
	x0, y0 := float64(p.x*2), float64(p.y*4)
	x1, y1 := float64((p.x+p.w)*2-1), float64((p.y+p.h)*4-1)
	ax, ay := math.Min(math.Max(s.x, x0-1), x1+1), math.Min(math.Max(s.y, y0-1), y1+1)
	dx, dy := ax-s.x, (ay-s.y)*ky
	l := math.Hypot(dx, dy)
	if l < 4 {
		return
	}
	// From the halo's ring outward.
	sx, sy := s.x+dx/l*3.6, s.y+dy/l*3.6/ky
	d.Line(sx, sy, ax, ay, c, 2, 0)
}

// drawPlate draws a label: a plate with a spine in its site's color, its
// title and a row per node, or one line with a glyph per node.
func (m *Model) drawPlate(cv *Canvas, ox, oy int, p plate, nodes []mapNode, f mapFocus, runs map[string][]span) {
	s := p.site
	lead := s.nodes[0]
	for _, i := range s.nodes {
		if f.hasNode(nodes[i].ID) {
			lead = i
			break
		}
	}
	x, y := ox+p.x, oy+p.y
	if p.location {
		paper, text := Mix(cInk, cKeyBG, .4), cText
		if s.focused || s.onRoute {
			paper, text = cSelBG, cGlow
		}
		st := fg(text).Bg(paper).bold()
		cv.Fill(x, y, p.w, 1, Style{BG: paper})
		cv.Text(x+1, y, p.title, st, p.w-2)
		if m.NoColor && (s.focused || s.onRoute) {
			cv.Set(x, y, '›', st)
		}
		m.hit(x, y, p.w, "select:node:"+nodes[lead].ID)
		return
	}
	if p.line {
		ts, bg := fg(cText), Color(0)
		if s.focused || s.onRoute {
			ts, bg = fg(cGlow).Bg(cSelBG).bold(), cSelBG
		}
		n := cv.Text(x, y, p.title, ts, -1)
		if len(s.nodes) > 1 {
			cv.Set(x+n, y, ' ', Style{BG: bg})
			for k, i := range s.nodes[:min(len(s.nodes), 5)] {
				sig := nodeSignal(nodes[i])
				cv.Set(x+n+1+k, y, signalGlyph(sig), fg(signalColor(sig)).Bg(bg))
			}
			if len(s.nodes) > 5 {
				cv.Text(x+n+6, y, "+"+itoa(len(s.nodes)-5), fg(cMuted).Bg(bg), -1)
			}
		}
		m.hit(x, y, p.w, "select:node:"+nodes[lead].ID)
		return
	}
	paper := Mix(cInk, cKeyBG, .62)
	if s.focused || s.onRoute {
		paper = Mix(cKeyBG, cSelBG, .5)
	}
	spine := Mix(signalColor(s.signal), paper, .25)
	cv.Fill(x, y, p.w, p.h, Style{BG: paper})
	for r := 0; r < p.h; r++ {
		cv.Set(x, y+r, '▎', fg(spine).Bg(paper))
	}
	inner := p.w - 3
	cv.Text(x+2, y, ellipsis(p.title, inner), fg(cText).Bg(paper).bold(), inner)
	m.hit(x, y, p.w, "select:node:"+nodes[lead].ID)
	for r, i := range p.rows {
		n, yy := nodes[i], y+1+r
		bg, st := paper, fg(Mix(cText, cMuted, .15)).Bg(paper)
		if p.depth[r] > 0 {
			st = fg(Mix(cText, cMuted, .45)).Bg(paper)
		}
		if f.hasNode(n.ID) {
			bg = cSelBG
			st = fg(cGlow).Bg(bg).bold()
			cv.Fill(x+1, yy, p.w-1, 1, Style{BG: bg})
			if m.NoColor {
				cv.Set(x, yy, '›', st) // without color, the mark is the selection
			}
		}
		cx := x + 2 + 2*p.depth[r]
		if p.depth[r] > 0 {
			cv.Set(cx-2, yy, '└', fg(cFaint).Bg(bg))
		}
		sig := nodeSignal(n)
		cv.Set(cx, yy, signalGlyph(sig), fg(signalColor(sig)).Bg(bg).bold())
		if room := x + p.w - 1 - (cx + 2); room > 0 {
			if k := cv.Text(cx+2, yy, ellipsis(n.Name, room), st, room); k == width(n.Name) {
				drawRuns(cv, cx+2+k, yy, room-k, runs[n.ID], bg, false)
			}
		}
		m.hit(x, yy, p.w, "select:node:"+n.ID)
	}
	if p.more > 0 {
		cv.Text(x+2, y+p.h-1, "+"+itoa(p.more)+" more", fg(cMuted).Bg(paper), inner)
	}
}
