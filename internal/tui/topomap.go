package tui

import (
	"math"
	"sort"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type mapNode struct {
	ID, Name, Kind, Group, Health, Place string
	Lat, Lon                             float64
	Located, Inherited, Affected         bool
	order                                int
}
type mapEdge struct {
	ID, From, To, Health, Label string
	Kind                        int    // edgeLink, edgePath, edgeContain
	Signal                      string // what the map may claim; see linkSignal
	Observer                    string // for path-implied edges: whose path
}

const (
	edgeLink = iota
	edgePath
	edgeContain
)

// Signals are what a map element may honestly claim:
//
//	up        the element's own checks pass
//	carried   not measured itself, but a declared path over it succeeds end to end
//	affected  on a failing path or dependency; the faulty segment is not established
//	degraded  mixed or unconfirmed evidence
//	down      the element's own checks confirm failure
//	unknown   no current evidence
//
// Cached views and drafts carry no evidence, so everything there is unknown.
func signalRank(s string) int {
	switch s {
	case "down":
		return 5
	case "degraded":
		return 4
	case "affected":
		return 3
	case "up", "carried":
		return 2
	case "unknown":
		return 1
	}
	return 0
}

// worseSignal keeps the signal that needs more attention; between equally
// ranked signals a measured "up" beats an inferred "carried".
func worseSignal(a, b string) string {
	if signalRank(b) > signalRank(a) || signalRank(b) == signalRank(a) && b == "up" {
		return b
	}
	return a
}

func signalColor(s string) Color {
	switch s {
	case "up", "carried":
		return cUp
	case "down":
		return cDown
	case "degraded", "affected":
		return cWarn
	}
	return cUnknown
}

func signalGlyph(s string) rune {
	switch s {
	case "affected":
		return '▲'
	case "carried":
		return '●'
	}
	return healthGlyph(s)
}

func nodeSignal(n mapNode) string {
	if n.Affected && (n.Health == "up" || n.Health == "unknown") {
		return "affected"
	}
	return n.Health
}

// graph derives drawable nodes/edges from the snapshot. Path-implied edges are
// declared expectations (dashed), never presented as measured links.
func (m *Model) graph() ([]mapNode, []mapEdge, map[string]Color) {
	s := m.Snapshot
	cached := m.Cached()
	type state struct {
		health   string
		affected bool
	}
	states := func(list []model.EntityState) map[string]state {
		out := map[string]state{}
		for _, e := range list {
			out[e.ID] = state{e.Health, e.Affected}
		}
		return out
	}
	health := func(list map[string]state, id string) (string, bool) {
		if h, ok := list[id]; ok && !cached {
			return h.health, h.affected
		}
		return "unknown", false
	}
	nodeStates := states(s.Nodes)
	nodes := []mapNode{}
	index := map[string]int{}
	groups := map[string]Color{}
	for i, n := range s.Topology.Nodes {
		name := n.Name
		if name == "" {
			name = n.ID
		}
		if _, ok := groups[n.Group]; !ok {
			groups[n.Group] = groupHues[len(groups)%len(groupHues)]
		}
		h, affected := health(nodeStates, n.ID)
		mn := mapNode{ID: n.ID, Name: name, Kind: n.Kind, Group: n.Group, Health: h, Affected: affected, order: i}
		if n.Location != nil {
			mn.Lat, mn.Lon, mn.Located, mn.Place = n.Location.Lat, n.Location.Lon, true, n.Location.Label
		}
		index[n.ID] = len(nodes)
		nodes = append(nodes, mn)
	}
	// Children without their own placement share the parent's declared site.
	for range 8 {
		for i, n := range s.Topology.Nodes {
			if nodes[i].Located || n.Parent == "" {
				continue
			}
			if p, ok := index[n.Parent]; ok && nodes[p].Located {
				nodes[i].Lat, nodes[i].Lon, nodes[i].Place = nodes[p].Lat, nodes[p].Lon, nodes[p].Place
				nodes[i].Located, nodes[i].Inherited = true, true
			}
		}
	}
	// A link no check measures is "carried" when a declared path over it
	// succeeds end to end in this view.
	pathStates := states(s.Paths)
	carried := map[string]bool{}
	if !cached {
		for _, p := range s.Topology.Paths {
			if st, ok := pathStates[p.ID]; ok && st.health == "up" {
				for _, l := range p.Links {
					carried[l] = true
				}
			}
		}
	}
	edges := []mapEdge{}
	seen := map[[2]string]bool{}
	linkStates := states(s.Links)
	for _, l := range s.Topology.Links {
		if _, a := index[l.From]; !a {
			continue
		}
		if _, b := index[l.To]; !b {
			continue
		}
		h, affected := health(linkStates, l.ID)
		sig := h
		switch {
		case h != "unknown":
		case affected:
			sig = "affected"
		case carried[l.ID]:
			sig = "carried"
		}
		edges = append(edges, mapEdge{ID: l.ID, From: l.From, To: l.To, Health: h, Label: l.Transport, Kind: edgeLink, Signal: sig})
		seen[[2]string{l.From, l.To}] = true
		seen[[2]string{l.To, l.From}] = true
	}
	for _, p := range s.Topology.Paths {
		h, _ := health(pathStates, p.ID)
		sig := map[string]string{"up": "carried", "down": "down", "degraded": "affected"}[h]
		if sig == "" {
			sig = "unknown"
		}
		for i := 1; i < len(p.Nodes); i++ {
			a, b := p.Nodes[i-1], p.Nodes[i]
			_, okA := index[a]
			_, okB := index[b]
			if a == b || !okA || !okB || seen[[2]string{a, b}] {
				continue
			}
			// Several paths may imply the same hop; keep the one that needs
			// the most attention.
			dup := false
			for k := range edges {
				e := &edges[k]
				if e.Kind == edgePath && (e.From == a && e.To == b || e.From == b && e.To == a) {
					if worseSignal(e.Signal, sig) != e.Signal {
						e.Signal, e.Health, e.ID, e.Observer = sig, h, "path:"+p.ID, p.Observer
					}
					dup = true
				}
			}
			if !dup {
				edges = append(edges, mapEdge{ID: "path:" + p.ID, From: a, To: b, Health: h, Label: "declared in path " + p.ID, Kind: edgePath, Signal: sig, Observer: p.Observer})
			}
		}
	}
	for _, n := range s.Topology.Nodes {
		if _, ok := index[n.Parent]; ok && n.Parent != "" {
			edges = append(edges, mapEdge{ID: "parent:" + n.ID, From: n.Parent, To: n.ID, Health: "unknown", Kind: edgeContain, Signal: "unknown"})
		}
	}
	return nodes, edges, groups
}

// focus reports which node and edge IDs the current selection focuses.
func (m *Model) focus(sel Row) (node, edge string) {
	switch sel.Kind {
	case "node":
		return sel.ID, ""
	case "link":
		return "", sel.ID
	case "endpoint":
		for _, e := range m.Snapshot.Topology.Endpoints {
			if e.ID == sel.ID {
				return e.Node, ""
			}
		}
	}
	return "", ""
}

func (m *Model) mapMode() string {
	if m.MapMode != "" {
		return m.MapMode
	}
	for _, n := range m.Snapshot.Topology.Nodes {
		if n.Location != nil {
			return "geo"
		}
	}
	return "network"
}

// mapChrome is the rows a map gives to its legend and "no location" tray.
func (m *Model) mapChrome() int {
	for _, n := range m.Snapshot.Topology.Nodes {
		if n.Location == nil && n.Parent == "" {
			return 2
		}
	}
	return 1
}

func (m *Model) drawMap(cv *Canvas, x, y, w, h int, sel Row) {
	if w < 8 || h < 3 {
		return
	}
	nodes, edges, groups := m.graph()
	focusNode, focusEdge := m.focus(sel)
	legend := h >= 6
	mh := h
	if legend {
		mh--
	}
	if m.mapMode() == "geo" {
		m.drawGeo(cv, x, y, w, mh, nodes, edges, focusNode, focusEdge)
	} else {
		m.drawNetwork(cv, x, y, w, mh, nodes, edges, groups, focusNode, focusEdge)
	}
	if legend {
		ly := y + h - 1
		cx := x + 1
		item := func(r rune, c Color, label string) {
			if cx+2+width(label) >= x+w {
				return
			}
			cv.Set(cx, ly, r, fg(c))
			cx += 1 + cv.Text(cx+1, ly, label, fg(cMuted), -1) + 2
		}
		item('●', cUp, "up")
		item('▲', cWarn, "warn")
		item('✖', cDown, "down")
		item('○', cUnknown, "unknown")
		cx += 2
		item('━', Mix(cUp, cInk, .3), "path ok")
		item('━', cWarn, "affected")
		item('━', cDown, "failed")
		item('┄', Mix(cUnknown, cInk, .2), "no evidence")
		if m.mapMode() == "network" {
			item('┊', cFaint, "hosted in")
			cx += 2
			names := []string{}
			for _, n := range nodes {
				if n.Group != "" && !contains(names, n.Group) {
					names = append(names, n.Group)
				}
			}
			for _, g := range names {
				item('■', groups[g], g)
			}
		}
	}
}

func edgeStyle(e mapEdge, focusNode, focusEdge string) (Color, int8, int) {
	var c Color
	pri, dash := int8(3), 0
	switch e.Signal {
	case "up", "carried":
		c, pri = Mix(cUp, cInk, .48), 4
	case "affected", "degraded":
		c, pri = Mix(cWarn, cInk, .12), 5
	case "down":
		c, pri = cDown, 6
	default:
		c = Mix(cUnknown, cInk, .3)
	}
	switch e.Kind {
	case edgePath:
		dash = 2
		if e.Signal == "unknown" || e.Signal == "carried" {
			c, pri = Mix(c, cInk, .3), pri-1
		}
	case edgeContain:
		c, pri, dash = Mix(cFaint, cInk, .2), 2, 1
	}
	if e.ID == focusEdge || focusNode != "" && (e.From == focusNode || e.To == focusNode) {
		c = Mix(c, cGlow, .4)
		pri = 8
	}
	return c, pri, dash
}

// pulse sends comets along edges whose signal is up or carried; edges with no
// evidence or with failing evidence stay still, so motion itself means
// "traffic is getting through".
func (m *Model) pulse(d *Dots, path [][2]float64, e mapEdge, phase float64, reverse bool) {
	if e.Signal != "up" && e.Signal != "carried" || len(path) < 6 || e.Kind == edgeContain {
		return
	}
	n := float64(len(path))
	count := max(1, int(n/48))
	off := float64(hash32(e.ID)%1000) / 1000 * n
	for c := 0; c < count; c++ {
		head := int(math.Mod(phase*16+off+float64(c)*n/float64(count), n))
		for k := 0; k < 5 && head-k >= 0; k++ {
			i := head - k
			if reverse {
				i = len(path) - 1 - i
			}
			p := path[i]
			d.Set(int(math.Round(p[0])), int(math.Round(p[1])), Mix(cGlow, cUp, float64(k)/5), 9)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// --- network view ----------------------------------------------------------

// layers assigns longest-path depth over links, path adjacency and hosting;
// cycles are broken at DFS back edges so any declared topology can be drawn.
func layers(nodes []mapNode, edges []mapEdge) map[string]int {
	adj := map[string][]string{}
	deg := map[string]int{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		deg[e.From]++
		deg[e.To]++
	}
	state := map[string]int{}
	order := []string{}
	back := map[[2]string]bool{}
	var visit func(string)
	visit = func(v string) {
		state[v] = 1
		for _, u := range adj[v] {
			switch state[u] {
			case 0:
				visit(u)
			case 1:
				back[[2]string{v, u}] = true
			}
		}
		state[v] = 2
		order = append(order, v)
	}
	for _, n := range nodes {
		if state[n.ID] == 0 && deg[n.ID] > 0 {
			visit(n.ID)
		}
	}
	layer := map[string]int{}
	maxL := 0
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		for _, u := range adj[v] {
			if !back[[2]string{v, u}] && layer[v]+1 > layer[u] {
				layer[u] = layer[v] + 1
			}
		}
	}
	for _, n := range nodes {
		maxL = max(maxL, layer[n.ID])
	}
	for _, n := range nodes {
		if deg[n.ID] == 0 {
			layer[n.ID] = maxL
		}
	}
	return layer
}

func (m *Model) drawNetwork(cv *Canvas, x, y, w, h int, nodes []mapNode, edges []mapEdge, groups map[string]Color, focusNode, focusEdge string) {
	if len(nodes) == 0 {
		msg := "Empty topology — press a to add a node"
		cv.Text(x+max(0, (w-width(msg))/2), y+h/2, msg, m.st(fg(cMuted).italic()), w)
		return
	}
	layer := layers(nodes, edges)
	nl := 0
	for _, l := range layer {
		nl = max(nl, l+1)
	}
	cols := make([][]int, nl)
	groupRank := map[string]int{}
	for _, n := range nodes {
		if _, ok := groupRank[n.Group]; !ok {
			groupRank[n.Group] = len(groupRank)
		}
	}
	for i, n := range nodes {
		cols[layer[n.ID]] = append(cols[layer[n.ID]], i)
	}
	for _, c := range cols {
		sort.SliceStable(c, func(a, b int) bool {
			return groupRank[nodes[c[a]].Group] < groupRank[nodes[c[b]].Group]
		})
	}
	// Barycentric ordering against neighbours reduces crossings.
	idx := map[string]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}
	rowOf := make([]float64, len(nodes))
	setRows := func() {
		for _, c := range cols {
			for r, i := range c {
				rowOf[i] = (float64(r) + .5) / float64(len(c))
			}
		}
	}
	setRows()
	for iter := 0; iter < 6; iter++ {
		for _, c := range cols {
			bary := map[int]float64{}
			for _, i := range c {
				sum, n := 0.0, 0.0
				for _, e := range edges {
					var o string
					if e.From == nodes[i].ID {
						o = e.To
					} else if e.To == nodes[i].ID {
						o = e.From
					} else {
						continue
					}
					sum += rowOf[idx[o]]
					n++
				}
				bary[i] = rowOf[i]
				if n > 0 {
					bary[i] = (sum/n + rowOf[i]) / 2
				}
			}
			sort.SliceStable(c, func(a, b int) bool { return bary[c[a]] < bary[c[b]] })
		}
		setRows()
	}
	colW := w / nl
	type box struct{ x, y, w int }
	boxes := make([]box, len(nodes))
	for l, c := range cols {
		n := len(c)
		for r, i := range c {
			by := int((float64(r) + .5) * float64(h) / float64(n))
			lw := min(width(nodes[i].Name)+2, colW-3)
			if l == nl-1 {
				lw = min(width(nodes[i].Name)+2, w-l*colW-1)
			}
			boxes[i] = box{x: l*colW + 1, y: min(by, h-1), w: max(lw, 3)}
		}
	}
	d := NewDots(w, h)
	phase := m.phase()
	type arrow struct {
		x, y int
		c    Color
	}
	arrows := []arrow{}
	for _, e := range edges {
		a, b := boxes[idx[e.From]], boxes[idx[e.To]]
		c, pri, dash := edgeStyle(e, focusNode, focusEdge)
		var path [][2]float64
		p0 := [2]float64{float64((a.x+a.w)*2 + 1), float64(a.y*4 + 2)}
		p3 := [2]float64{float64((b.x-1)*2 - 1), float64(b.y*4 + 2)}
		switch {
		case layer[e.From] == layer[e.To] && e.Kind == edgeContain:
			// Stacked in one column: a bracket on the left margin.
			q0 := [2]float64{float64(a.x*2 - 1), p0[1]}
			q3 := [2]float64{float64(b.x*2 - 1), p3[1]}
			path = curve(q0, [2]float64{q0[0] - 4, q0[1]}, [2]float64{q3[0] - 4, q3[1]}, q3)
		case layer[e.To] <= layer[e.From]:
			// Back edge: loop around the right side of both labels, dashed.
			p3 = [2]float64{float64((b.x+b.w)*2 + 1), p3[1]}
			bend := math.Max(p0[0], p3[0]) + 8
			path = curve(p0, [2]float64{bend, p0[1]}, [2]float64{bend, p3[1]}, p3)
			dash = max(dash, 2)
		default:
			tan := math.Min((p3[0]-p0[0])/2, 16)
			path = curve(p0, [2]float64{p0[0] + tan, p0[1]}, [2]float64{p3[0] - tan, p3[1]}, p3)
			if e.Kind != edgeContain {
				arrows = append(arrows, arrow{b.x - 1, b.y, c})
			}
		}
		d.Polyline(path, c, pri, dash)
		m.pulse(d, path, e, phase, false)
	}
	d.Blit(cv, x, y, 0)
	for _, a := range arrows {
		cv.Set(x+a.x, y+a.y, '▸', m.st(fg(a.c)))
	}
	// Faint column headers: hop depth from origin nodes.
	if h > 8 && nl > 1 {
		for l := 0; l < nl; l++ {
			label := "hop " + itoa(l)
			if l == 0 {
				label = "origin"
			}
			cv.Text(x+l*colW+1, y, label, m.st(fg(cFaint)), colW-2)
		}
	}
	for i, n := range nodes {
		b := boxes[i]
		focused := n.ID == focusNode
		st := fg(groups[n.Group])
		if n.Kind == "external" {
			st = st.italic()
		}
		if focused {
			st = fg(cGlow).Bg(cSelBG).bold()
		}
		cv.Set(x+b.x, y+b.y, healthGlyph(n.Health), m.st(fg(healthColor(n.Health)).Bg(st.BG).bold()))
		cv.Pad(x+b.x+1, y+b.y, " "+n.Name, m.st(st), b.w-1)
		m.hit(x+b.x, y+b.y, b.w, "select:node:"+n.ID)
	}
	tag := " network "
	cv.Text(x+w-width(tag), y, tag, m.st(fg(cFaint)), -1)
}

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
