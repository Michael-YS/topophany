package tui

import (
	"math"
	"sort"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type mapNode struct {
	ID, Name, Kind, Group, Health, Place string
	Lat, Lon                             float64
	Located, Inherited                   bool
	order                                int
}
type mapEdge struct {
	ID, From, To, Health, Label string
	Kind                        int // edgeLink, edgePath, edgeContain
}

const (
	edgeLink = iota
	edgePath
	edgeContain
)

// graph derives drawable nodes/edges from the snapshot. Path-implied edges are
// declared expectations (dashed), never presented as measured links.
func (m *Model) graph() ([]mapNode, []mapEdge, map[string]Color) {
	s := m.Snapshot
	cached := m.Cached()
	states := func(list []model.EntityState) map[string]string {
		out := map[string]string{}
		for _, e := range list {
			out[e.ID] = e.Health
		}
		return out
	}
	health := func(list map[string]string, id string) string {
		if h, ok := list[id]; ok && !cached {
			return h
		}
		return "unknown"
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
		mn := mapNode{ID: n.ID, Name: name, Kind: n.Kind, Group: n.Group, Health: health(nodeStates, n.ID), order: i}
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
		edges = append(edges, mapEdge{ID: l.ID, From: l.From, To: l.To, Health: health(linkStates, l.ID), Label: l.Transport, Kind: edgeLink})
		seen[[2]string{l.From, l.To}] = true
		seen[[2]string{l.To, l.From}] = true
	}
	pathStates := states(s.Paths)
	for _, p := range s.Topology.Paths {
		for i := 1; i < len(p.Nodes); i++ {
			a, b := p.Nodes[i-1], p.Nodes[i]
			_, okA := index[a]
			_, okB := index[b]
			if a == b || !okA || !okB || seen[[2]string{a, b}] {
				continue
			}
			seen[[2]string{a, b}] = true
			seen[[2]string{b, a}] = true
			edges = append(edges, mapEdge{ID: "path:" + p.ID, From: a, To: b, Health: health(pathStates, p.ID), Label: "declared in path " + p.ID, Kind: edgePath})
		}
	}
	for _, n := range s.Topology.Nodes {
		if _, ok := index[n.Parent]; ok && n.Parent != "" {
			edges = append(edges, mapEdge{ID: "parent:" + n.ID, From: n.Parent, To: n.ID, Health: "unknown", Kind: edgeContain})
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
		m.drawGeo(cv, x, y, w, mh, nodes, edges, groups, focusNode, focusEdge)
	} else {
		m.drawNetwork(cv, x, y, w, mh, nodes, edges, groups, focusNode, focusEdge)
	}
	if legend {
		ly := y + h - 1
		cx := x
		item := func(r rune, c Color, label string) {
			if cx+2+width(label) >= x+w {
				return
			}
			cv.Set(cx, ly, r, fg(c))
			cx += 1 + cv.Text(cx+1, ly, label, m.st(fg(cMuted)), -1) + 2
		}
		item('●', cUp, "up")
		item('✖', cDown, "down")
		item('▲', cWarn, "warn")
		item('○', cUnknown, "unknown")
		item('─', cAccent2, "link")
		item('┄', cMuted, "declared path")
		if m.mapMode() == "network" {
			item('┊', cFaint, "hosted in")
		}
		cx++
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

// --- geographic view -------------------------------------------------------

type geoView struct{ lon0, lat0, scale float64 }

func (g geoView) project(lat, lon float64) (float64, float64) {
	d := math.Mod(lon-g.lon0, 360)
	if d < 0 {
		d += 360
	}
	return d / g.scale, (g.lat0 - lat) / g.scale
}
func (g geoView) unproject(px, py float64) (float64, float64) {
	return g.lat0 - py*g.scale, g.lon0 + px*g.scale
}

// fitGeo chooses the window around the antimeridian-aware bounding box of the
// placed nodes, e.g. Shanghai + Los Angeles spans the Pacific, not Europe.
func fitGeo(points [][2]float64, wd, hd float64, zoom int, focus *[2]float64) geoView {
	world := geoView{lon0: -180, scale: math.Max(360/wd, 160/hd)}
	world.lat0 = 8 + hd*world.scale/2
	if zoom == 2 || len(points) == 0 && focus == nil {
		return clampLat(world, hd)
	}
	var cLat, cLon, spanLat, spanLon float64
	if zoom == 1 && focus != nil {
		cLat, cLon, spanLat, spanLon = focus[0], focus[1], 22, 36
	} else {
		lons := make([]float64, 0, len(points))
		minLat, maxLat := 90.0, -90.0
		for _, p := range points {
			lons = append(lons, p[1])
			minLat, maxLat = math.Min(minLat, p[0]), math.Max(maxLat, p[0])
		}
		sort.Float64s(lons)
		gap, start := 360-(lons[len(lons)-1]-lons[0]), lons[0]
		for i := 1; i < len(lons); i++ {
			if g := lons[i] - lons[i-1]; g > gap {
				gap, start = g, lons[i]
			}
		}
		spanLon = 360 - gap
		cLon = start + spanLon/2
		cLat = (minLat + maxLat) / 2
		spanLat = maxLat - minLat
		spanLon = math.Max(spanLon+math.Max(28, spanLon*.18), 50)
		spanLat = math.Max(spanLat+math.Max(18, spanLat*.3), 28)
	}
	scale := math.Max(spanLon/wd, spanLat/hd)
	scale = math.Min(scale, world.scale)
	g := geoView{lon0: cLon - wd*scale/2, lat0: cLat + hd*scale/2, scale: scale}
	return clampLat(g, hd)
}
func clampLat(g geoView, hd float64) geoView {
	if span := hd * g.scale; span < 180 {
		g.lat0 = math.Min(g.lat0, 84)
		g.lat0 = math.Max(g.lat0, -84+span)
	}
	return g
}

func (m *Model) drawGeo(cv *Canvas, x, y, w, h int, nodes []mapNode, edges []mapEdge, groups map[string]Color, focusNode, focusEdge string) {
	unplaced := []int{}
	pts := [][2]float64{}
	var focus *[2]float64
	for i, n := range nodes {
		if n.Located {
			pts = append(pts, [2]float64{n.Lat, n.Lon})
			if n.ID == focusNode {
				focus = &[2]float64{n.Lat, n.Lon}
			}
		} else {
			unplaced = append(unplaced, i)
		}
	}
	mh := h
	if len(unplaced) > 0 && h > 4 {
		mh--
	}
	if focus == nil && len(pts) > 0 && m.Zoom == 1 {
		focus = &pts[0]
	}
	d := NewDots(w, mh)
	wd, hd := float64(w*2), float64(mh*4)
	g := fitGeo(pts, wd, hd, m.Zoom, focus)
	isLand := func(px, py int) bool {
		lat, lon := g.unproject(float64(px)+.5, float64(py)+.5)
		return lat > -90 && lat < 90 && land(lat, lon)
	}
	grid := math.Max(15, math.Round(g.scale*24/15)*15)
	for py := 0; py < mh*4; py++ {
		for px := 0; px < w*2; px++ {
			lat, lon := g.unproject(float64(px)+.5, float64(py)+.5)
			if lat <= -90 || lat >= 90 {
				continue
			}
			if land(lat, lon) {
				coast := !isLand(px-1, py) || !isLand(px+1, py) || !isLand(px, py-1) || !isLand(px, py+1)
				if coast {
					d.Set(px, py, cCoast, 1)
				} else if px%2 == 0 && py%2 == 0 {
					d.Set(px, py, cLand, 1)
				}
				continue
			}
			// Faint graticule: dotted meridians/parallels on the ocean only.
			onLat := math.Abs(math.Mod(lat+900, grid)) < g.scale
			onLon := math.Abs(math.Mod(lon+1800, grid)) < g.scale
			if (onLat && px%4 == 0) || (onLon && py%4 == 0) {
				d.Set(px, py, cOcean, 0)
			}
		}
	}
	pos := map[string][2]float64{}
	for _, n := range nodes {
		if n.Located {
			px, py := g.project(n.Lat, n.Lon)
			pos[n.ID] = [2]float64{px, py}
		}
	}
	tray := map[string][2]float64{}
	if len(unplaced) > 0 && h > 4 {
		tx := 12
		for _, i := range unplaced {
			tray[nodes[i].ID] = [2]float64{float64(tx*2 + 1), float64(mh*4 + 1)}
			tx += width(nodes[i].Name) + 4
		}
	}
	anchor := func(id string) ([2]float64, bool) {
		if p, ok := pos[id]; ok {
			return p, true
		}
		p, ok := tray[id]
		return p, ok
	}
	phase := m.phase()
	for _, e := range edges {
		if e.Kind == edgeContain {
			continue
		}
		a, okA := anchor(e.From)
		b, okB := anchor(e.To)
		if !okA || !okB {
			continue
		}
		if math.Hypot(a[0]-b[0], a[1]-b[1]) < 3 {
			continue
		}
		// Lines to the "no location" tray are a UI convention, not geography:
		// draw them only for the focused node to keep the map readable.
		_, placedA := pos[e.From]
		_, placedB := pos[e.To]
		if (!placedA || !placedB) && e.From != focusNode && e.To != focusNode {
			continue
		}
		c, pri, dash := edgeStyle(e, focusNode, focusEdge)
		// Flight-path arc: bow perpendicular to the chord, always "upward".
		mx, my := (a[0]+b[0])/2, (a[1]+b[1])/2
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := math.Hypot(dx, dy)
		nx, ny := -dy/l, dx/l
		if ny > 0 {
			nx, ny = -nx, -ny
		}
		bow := math.Min(l*.28, hd*.3)
		cp := [2]float64{mx + nx*bow, my + ny*bow}
		p1 := [2]float64{a[0] + (cp[0]-a[0])*2/3, a[1] + (cp[1]-a[1])*2/3}
		p2 := [2]float64{b[0] + (cp[0]-b[0])*2/3, b[1] + (cp[1]-b[1])*2/3}
		path := curve(a, p1, p2, b)
		d.Polyline(path, c, pri, dash)
		m.pulse(d, path, e, phase)
	}
	d.Blit(cv, x, y, 0)
	// Markers: nodes sharing a cell at this zoom are clustered.
	type cluster struct {
		cx, cy  int
		members []int
	}
	clusters := []*cluster{}
	byCell := map[[2]int]*cluster{}
	for i, n := range nodes {
		p, ok := pos[n.ID]
		if !ok {
			continue
		}
		k := [2]int{int(p[0]) / 2, int(p[1]) / 4}
		if k[0] < 0 || k[1] < 0 || k[0] >= w || k[1] >= mh {
			continue
		}
		c := byCell[k]
		if c == nil {
			c = &cluster{cx: k[0], cy: k[1]}
			byCell[k] = c
			clusters = append(clusters, c)
		}
		c.members = append(c.members, i)
	}
	occupied := make([]bool, w*h)
	occupy := func(cx, cy, n int) {
		for i := cx; i < cx+n; i++ {
			if i >= 0 && i < w && cy >= 0 && cy < h {
				occupied[cy*w+i] = true
			}
		}
	}
	free := func(cx, cy, n int) bool {
		if cy < 0 || cy >= mh || cx < 0 || cx+n > w {
			return false
		}
		for i := cx; i < cx+n; i++ {
			if occupied[cy*w+i] {
				return false
			}
		}
		return true
	}
	for _, c := range clusters {
		occupy(c.cx, c.cy, 1)
	}
	// Focused cluster first so it always gets a label.
	sort.SliceStable(clusters, func(i, j int) bool {
		has := func(c *cluster) bool {
			for _, k := range c.members {
				if nodes[k].ID == focusNode {
					return true
				}
			}
			return false
		}
		return has(clusters[i]) && !has(clusters[j])
	})
	for _, c := range clusters {
		worst, lead, focused := "up", c.members[0], false
		for _, k := range c.members {
			worst = worse(worst, nodes[k].Health)
			if nodes[k].ID == focusNode {
				lead, focused = k, true
			}
		}
		n := nodes[lead]
		marker := healthGlyph(worst)
		if len(c.members) > 1 {
			marker = '◉'
		}
		st := fg(healthColor(worst)).bold()
		if focused {
			st = st.Bg(cSelBG)
		}
		cv.Set(x+c.cx, y+c.cy, marker, m.st(st))
		m.hit(x+c.cx, y+c.cy, 1, "select:node:"+n.ID)
		label := n.Name
		if len(c.members) > 1 {
			if n.Place != "" && !focused {
				label = n.Place
			}
			label += " +" + itoa(len(c.members)-1)
		}
		lw := width(label)
		for _, cand := range [][2]int{{c.cx + 2, c.cy}, {c.cx - 1 - lw, c.cy}, {c.cx + 1, c.cy - 1}, {c.cx + 1, c.cy + 1}, {c.cx - lw, c.cy - 1}, {c.cx - lw, c.cy + 1}} {
			if free(cand[0], cand[1], lw) {
				ls := fg(groups[n.Group])
				if focused {
					ls = fg(cGlow).Bg(cSelBG).bold()
				}
				cv.Text(x+cand[0], y+cand[1], label, m.st(ls), lw)
				m.hit(x+cand[0], y+cand[1], lw, "select:node:"+n.ID)
				occupy(cand[0]-1, cand[1], lw+2)
				break
			}
		}
	}
	if len(unplaced) > 0 && h > 4 {
		ty := y + mh
		cv.Text(x, ty, "no location", m.st(fg(cFaint).italic()), 11)
		for _, i := range unplaced {
			n := nodes[i]
			p := tray[n.ID]
			cx := x + int(p[0])/2
			st := fg(groups[n.Group])
			if n.ID == focusNode {
				st = fg(cGlow).Bg(cSelBG).bold()
			}
			cv.Set(cx-1, ty, healthGlyph(n.Health), m.st(fg(healthColor(n.Health))))
			k := cv.Text(cx, ty, n.Name, m.st(st), x+w-cx)
			m.hit(cx-1, ty, k+1, "select:node:"+n.ID)
		}
	}
	if len(pts) == 0 {
		msg := "No node locations declared — add one with e (edit) or press m for the network view"
		cv.Text(x+max(0, (w-width(msg))/2), y+mh/2, msg, m.st(fg(cMuted).italic()), w)
	}
	zoom := []string{"fit", "focus", "world"}[m.Zoom%3]
	tag := " geo · " + zoom + " "
	cv.Text(x+w-width(tag), y, tag, m.st(fg(cFaint)), -1)
}

func edgeStyle(e mapEdge, focusNode, focusEdge string) (Color, int8, int) {
	c := healthColor(e.Health)
	if e.Health == "unknown" {
		c = Mix(cAccent2, cDeep, .25)
	}
	pri, dash := int8(3), 0
	switch e.Kind {
	case edgePath:
		c = Mix(c, cDeep, .3)
		pri, dash = 2, 2
	case edgeContain:
		c, pri, dash = cFaint, 2, 1
	}
	if e.ID == focusEdge || focusNode != "" && (e.From == focusNode || e.To == focusNode) {
		c = Mix(c, cGlow, .45)
		pri = 6
	}
	return c, pri, dash
}

// pulse draws a travelling packet only on edges with fresh "up" evidence;
// unknown or failed segments are deliberately left static.
func (m *Model) pulse(d *Dots, path [][2]float64, e mapEdge, phase float64) {
	if e.Health != "up" || len(path) < 4 || e.Kind == edgeContain {
		return
	}
	t := math.Mod(phase*.45+float64(hash32(e.ID)%1000)/1000, 1)
	i := int(t * float64(len(path)-1))
	for k := 0; k < 3 && i-k >= 0; k++ {
		p := path[i-k]
		d.Set(int(math.Round(p[0])), int(math.Round(p[1])), Mix(cGlow, cUp, float64(k)/3), 9)
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

func worse(a, b string) string {
	rank := map[string]int{"up": 0, "unknown": 1, "degraded": 2, "down": 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
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
		m.pulse(d, path, e, phase)
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
