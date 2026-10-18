package tui

import (
	"math"
	"sort"
)

// geoLabels progressively removes detail without changing the map's framing.
type geoLabels uint8

const (
	geoCards geoLabels = iota
	geoLocations
	geoPoints
)

func (g geoLabels) next() geoLabels { return (g + 1) % 3 }
func (g geoLabels) name() string    { return [...]string{"Cards", "Locations", "Points"}[g] }

// The geographic view: declared node locations on a quiet dotted world map.
// Nodes that share a place become one site; links between sites are arcs
// colored by what the evidence allows them to claim, and only arcs whose
// traffic is getting through carry moving comets.

// geoView maps latitude/longitude to dot coordinates with an equirectangular
// projection whose horizontal scale kx = cos(centre latitude), so a
// mid-latitude network keeps its shape. Longitudes lon0+x0 .. lon0+x1 and
// latitudes lo .. hi exist on screen: a panel wider than the world shows it
// once, centred, and spare height is left empty rather than filled with
// Antarctica. Dots are ky times as tall as they are wide; scale is degrees
// per dot width, so the map keeps its shape whatever shape a cell is. Folds
// squeeze wide stretches of empty longitude, such as an ocean between two
// coasts, into narrow seams.
type geoView struct {
	lon0, lat0, scale, kx, ky, x0, x1, lo, hi float64
	folds                                     []fold
}

// fold squeezes the degrees a ≤ d < b east of a view's lon0 into seam
// degrees on screen.
type fold struct{ a, b, seam float64 }

// squeeze maps degrees east of lon0 to degrees across the screen.
func (g geoView) squeeze(d float64) float64 {
	out := d
	for _, f := range g.folds {
		switch {
		case d >= f.b:
			out -= f.b - f.a - f.seam
		case d > f.a:
			out -= (d - f.a) * (1 - f.seam/(f.b-f.a))
		}
	}
	return out
}

// unsqueeze is the inverse of squeeze.
func (g geoView) unsqueeze(s float64) float64 {
	shift := 0.0
	for _, f := range g.folds {
		a := f.a - shift
		switch {
		case s >= a+f.seam:
			shift += f.b - f.a - f.seam
		case s > a:
			return f.a + (s-a)*(f.b-f.a)/f.seam
		default:
			return s + shift
		}
	}
	return s + shift
}

func (g geoView) project(lat, lon float64) (float64, float64) {
	d := math.Mod(lon-g.lon0-g.x0, 360)
	if d < 0 {
		d += 360
	}
	return g.squeeze(d+g.x0) * g.kx / g.scale, (g.lat0 - lat) / g.scale / g.ky
}

// at is the inverse of project; ok is false off the world and in a fold's
// seam, which is left blank.
func (g geoView) at(px, py float64) (lat, lon float64, ok bool) {
	s := px * g.scale / g.kx
	d := g.unsqueeze(s)
	lat = g.lat0 - py*g.ky*g.scale
	for _, f := range g.folds {
		if a := g.squeeze(f.a); s > a && s < a+f.seam {
			return lat, g.lon0 + d, false
		}
	}
	return lat, g.lon0 + d, d >= g.x0 && d < g.x1 && lat >= g.lo && lat <= g.hi
}

// Gaps between sites wider than foldGap degrees of longitude are folded,
// keeping foldPad degrees beside each site and a seam foldSeam degrees wide.
const (
	foldGap  = 60.0
	foldPad  = 15.0
	foldSeam = 6.0
)

// lonBounds finds the smallest arc of longitude holding every point, so
// Shanghai + Los Angeles spans the Pacific rather than Europe.
func lonBounds(points [][2]float64) (centre, span float64) {
	lons := make([]float64, 0, len(points))
	for _, p := range points {
		lons = append(lons, math.Mod(math.Mod(p[1], 360)+540, 360)-180)
	}
	sort.Float64s(lons)
	gap, start := 360-(lons[len(lons)-1]-lons[0]), lons[0]
	for i := 1; i < len(lons); i++ {
		if g := lons[i] - lons[i-1]; g > gap {
			gap, start = g, lons[i]
		}
	}
	span = 360 - gap
	return start + span/2, span
}

// fitGeo frames the view: zoom 0 fits every placed node, 1 frames the focused
// node's region, 2 shows the world. Antarctica is left out unless a node is
// there, and the frame never zooms out past one world. Fitting every node,
// a wide gap between them folds: the longitudes it leaves empty, but for room
// beside the sites on either side, shrink to a seam, so those sites get the
// width. The view is wd×hd dots, each ky times as tall as it is wide.
func fitGeo(points [][2]float64, wd, hd, ky float64, zoom int, focus *[2]float64) geoView {
	return frameGeo(points, wd, hd, ky, zoom, focus, zoom == 0)
}

func frameGeo(points [][2]float64, wd, hd, ky float64, zoom int, focus *[2]float64, folding bool) geoView {
	hd *= ky // in dot widths from here on
	lo, hi := -56.0, 80.0
	for _, p := range points {
		lo, hi = math.Min(lo, p[0]-10), math.Max(hi, p[0]+10)
	}
	lo, hi = math.Max(lo, -90), math.Min(hi, 90)
	cLat, cLon := (lo+hi)/2, 10.0
	spanLat, spanLon := hi-lo, 360.0
	var folds []fold
	removed, lead := 0.0, 0.0
	if len(points) > 0 {
		var span float64
		cLon, span = lonBounds(points)
		minLat, maxLat := 90.0, -90.0
		for _, p := range points {
			minLat, maxLat = math.Min(minLat, p[0]), math.Max(maxLat, p[0])
		}
		if folding {
			// Degrees east of the sites' western end, where each gap starts
			// and ends.
			west := cLon - span/2
			ds := []float64{}
			for _, p := range points {
				ds = append(ds, math.Mod(math.Mod(p[1]-west+1e-9, 360)+360, 360))
			}
			sort.Float64s(ds)
			for i := 1; i < len(ds); i++ {
				if a, b := ds[i-1]+foldPad, ds[i]-foldPad; ds[i]-ds[i-1] > foldGap && b-a > 2*foldSeam {
					folds = append(folds, fold{a, b, foldSeam})
					removed += b - a - foldSeam
				}
			}
			span -= removed
			cLon = west + span/2 // in degrees across the screen from here on
		}
		switch {
		case zoom == 1 && focus != nil:
			cLat, cLon, spanLat, spanLon = focus[0], focus[1], 34, 60
		case zoom == 0:
			cLat = (minLat + maxLat) / 2
			spanLon = math.Max(span+math.Max(32, span*.2), 64)
			spanLat = math.Max(maxLat-minLat+math.Max(22, (maxLat-minLat)*.35), 32)
		}
		lead = span / 2
	}
	kx := clamp(math.Cos(cLat*math.Pi/180), .62, 1)
	whole := math.Max(360*kx/wd, (hi-lo)/hd)
	scale := math.Min(math.Max(spanLon*kx/wd, spanLat/hd), whole)
	vs := hd * scale
	if vs >= hi-lo {
		cLat = (lo + hi) / 2
	} else {
		cLat = clamp(cLat, lo+vs/2, hi-vs/2)
	}
	hs := wd * scale / kx
	g := geoView{lon0: cLon - hs/2, lat0: cLat + vs/2, scale: scale, kx: kx, ky: ky, x1: hs, lo: lo, hi: hi}
	if len(folds) > 0 {
		if hs >= 360-removed {
			// The whole world fits: nothing needs folding.
			return frameGeo(points, wd, hd/ky, ky, zoom, focus, false)
		}
		// The sites' western end stands hs/2 - lead east of lon0.
		for i := range folds {
			folds[i].a += hs/2 - lead
			folds[i].b += hs/2 - lead
		}
		g.folds = folds
		g.x1 = g.unsqueeze(hs)
		return g
	}
	if hs > 360 {
		g.x0, g.x1 = (hs-360)/2, (hs+360)/2
	}
	return g
}

// geoRows is how many map rows a w-cell-wide geographic view needs at the
// default fit: the latitudes its sites span, with room above and below for
// their labels, and never more than its whole latitude band. Layouts give
// any height beyond that to other panels rather than to empty sea and ice.
func (m *Model) geoRows(w int) int {
	nodes, edges, _ := m.graph()
	if !mapGraphAllowed(w, len(nodes), len(edges)) {
		return 0
	}
	pts := [][2]float64{}
	lo, hi := 90.0, -90.0
	for _, n := range nodes {
		if n.Located {
			pts = append(pts, [2]float64{n.Lat, n.Lon})
			lo, hi = math.Min(lo, n.Lat), math.Max(hi, n.Lat)
		}
	}
	ky := m.cellAspect() / 2
	g := fitGeo(pts, float64(w*2), 1e6, ky, 0, nil)
	band := g.hi - g.lo
	if len(pts) > 0 {
		band = math.Min(band, math.Max(hi-lo+48, 60))
	}
	return int(math.Ceil(band / g.scale / ky / 4))
}

// drawLand shades each cell by how much land it covers, then lays a regular
// dot lattice over the land and a dotted coastline along its edge. The shading
// carries the silhouette; the dots carry the texture. It returns how many of
// each cell's eight dots are land.
func (m *Model) drawLand(cv *Canvas, d *Dots, x, y, w, h int, g geoView) []uint8 {
	W, H := w*2, h*4
	grid := make([]bool, W*H)
	for py := 0; py < H; py++ {
		for px := 0; px < W; px++ {
			lat, lon, ok := g.at(float64(px)+.5, float64(py)+.5)
			grid[py*W+px] = ok && land(lat, lon)
		}
	}
	isLand := func(px, py int) bool {
		if px >= 0 && py >= 0 && px < W && py < H {
			return grid[py*W+px]
		}
		lat, lon, ok := g.at(float64(px)+.5, float64(py)+.5)
		return ok && land(lat, lon)
	}
	cells := make([]uint8, w*h)
	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < w; cx++ {
			n := uint8(0)
			for k := 0; k < 8; k++ {
				if grid[(cy*4+k/2)*W+cx*2+k%2] {
					n++
				}
			}
			cells[cy*w+cx] = n
			if n > 0 && !m.NoColor {
				cv.put(x+cx, y+cy, " ", 1, Style{BG: Mix(cInk, cLandFill, float64(n)/8)})
			}
		}
	}
	for py := 0; py < H; py++ {
		for px := 0; px < W; px++ {
			if !grid[py*W+px] {
				continue
			}
			if !isLand(px-1, py) || !isLand(px+1, py) || !isLand(px, py-1) || !isLand(px, py+1) {
				d.Set(px, py, cCoast, 1)
			} else if px%2 == 0 && py%2 == 0 {
				d.Set(px, py, cLand, 1)
			}
		}
	}
	return cells
}

type geoSite struct {
	x, y    float64 // dot coordinates
	nodes   []int
	places  []string
	signal  string
	focused bool // holds the selected node
	onRoute bool // holds a node of the selected path
}

// geoSites merges placed nodes into sites: the same declared place, or
// close enough on screen that their markers would collide.
func geoSites(nodes []mapNode, g geoView, f mapFocus) []*geoSite {
	sites := []*geoSite{}
	for i, n := range nodes {
		if !n.Located {
			continue
		}
		px, py := g.project(n.Lat, n.Lon)
		var s *geoSite
		for _, t := range sites {
			dx, dy := math.Abs(t.x-px), math.Abs(t.y-py)
			if n.Place != "" && contains(t.places, n.Place) && dx < 24 && dy < 24 || dx <= 5 && dy <= 4 {
				s = t
				break
			}
		}
		if s == nil {
			s = &geoSite{x: px, y: py}
			sites = append(sites, s)
		}
		if n.Place != "" && !contains(s.places, n.Place) {
			s.places = append(s.places, n.Place)
		}
		s.nodes = append(s.nodes, i)
		s.signal = worseSignal(s.signal, nodeSignal(n))
		s.focused = s.focused || n.ID == f.node
		s.onRoute = s.onRoute || f.nodes[n.ID]
	}
	for _, s := range sites {
		sx, sy := 0.0, 0.0
		for _, i := range s.nodes {
			px, py := g.project(nodes[i].Lat, nodes[i].Lon)
			sx, sy = sx+px, sy+py
		}
		s.x, s.y = sx/float64(len(s.nodes)), sy/float64(len(s.nodes))
	}
	return sites
}

// trimEnds drops curve points within r dots of either end so arcs stop short
// of the markers they connect.
func trimEnds(path [][2]float64, r float64) [][2]float64 {
	if len(path) < 2 {
		return path
	}
	a, b := path[0], path[len(path)-1]
	i, j := 0, len(path)
	for i < j && math.Hypot(path[i][0]-a[0], path[i][1]-a[1]) < r {
		i++
	}
	for j > i && math.Hypot(path[j-1][0]-b[0], path[j-1][1]-b[1]) < r {
		j--
	}
	return path[i:j]
}

// arcDash is how an arc is broken. The points view draws every arc dashed,
// so the map is markers and dashed lines whatever kind of edge an arc came from.
func arcDash(dash int, points bool) int {
	if points && dash < 2 {
		return 2
	}
	return dash
}

type geoArc struct {
	a, b                     int // site indices, a < b
	edge                     mapEdge
	declared, focused        bool
	always                   bool // drawn whatever is selected
	forward, backward        bool
	links, implied, problems int
}

func (m *Model) drawGeo(cv *Canvas, x, y, w, h int, nodes []mapNode, edges []mapEdge, f mapFocus) {
	if !mapLayoutAllowed(w, h, len(nodes), len(edges)) {
		drawLayoutLimit(cv, x, y, w, h, len(nodes), len(edges))
		return
	}
	unplaced := []int{}
	pts := [][2]float64{}
	var focus *[2]float64
	for i, n := range nodes {
		if n.Located {
			pts = append(pts, [2]float64{n.Lat, n.Lon})
			if n.ID == f.node || focus == nil && f.nodes[n.ID] {
				focus = &[2]float64{n.Lat, n.Lon}
			}
		} else {
			unplaced = append(unplaced, i)
		}
	}
	mh := h
	tray := len(unplaced) > 0 && h > 6
	if tray {
		mh--
	}
	if focus == nil && len(pts) > 0 && m.Zoom == 1 {
		focus = &pts[0]
	}
	W, H := float64(w*2), float64(mh*4)
	g := fitGeo(pts, W, H, m.cellAspect()/2, m.Zoom, focus)
	d := NewDots(w, mh)
	landCells := m.drawLand(cv, d, x, y, w, mh, g)
	sites := geoSites(nodes, g, f)
	siteOf := map[string]int{}
	for k, s := range sites {
		for _, i := range s.nodes {
			siteOf[nodes[i].ID] = k
		}
	}
	// One arc per pair of sites, carrying the signal that needs the most
	// attention. Hops implied only by declared paths stay off the map unless
	// they fail, are focused, or belong to the observer being viewed.
	arcs := map[[2]int]*geoArc{}
	for _, e := range edges {
		if e.Kind == edgeContain {
			continue
		}
		sa, okA := siteOf[e.From]
		sb, okB := siteOf[e.To]
		if !okA || !okB || sa == sb {
			continue
		}
		focused := f.hasEdge(e)
		if e.Kind == edgePath && !focused && signalRank(e.Signal) < 3 && (m.Observer == "" || e.Observer != m.Observer) {
			continue
		}
		key := [2]int{min(sa, sb), max(sa, sb)}
		a := arcs[key]
		if a == nil {
			a = &geoArc{a: key[0], b: key[1], edge: e}
			arcs[key] = a
		} else if worseSignal(a.edge.Signal, e.Signal) != a.edge.Signal {
			a.edge = e
		}
		if e.Kind == edgeLink {
			a.declared = true
		}
		a.always = a.always || e.Kind != edgePath || signalRank(e.Signal) >= 3 || m.Observer != "" && e.Observer == m.Observer
		a.focused = a.focused || focused
		if sa == key[0] {
			a.forward = true
		} else {
			a.backward = true
		}
	}
	order := make([]*geoArc, 0, len(arcs))
	for _, a := range arcs {
		order = append(order, a)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].a != order[j].a {
			return order[i].a < order[j].a
		}
		return order[i].b < order[j].b
	})
	phase := m.phase()
	arcCells := make([]bool, w*mh)
	for _, a := range order {
		A, B := sites[a.a], sites[a.b]
		p0, p3 := [2]float64{A.x, A.y}, [2]float64{B.x, B.y}
		dx, dy := p3[0]-p0[0], p3[1]-p0[1]
		l := math.Hypot(dx, dy)
		if l < 6 {
			continue
		}
		// Bow every arc toward the top of the map, like a flight path.
		nx, ny := -dy/l, dx/l
		if ny > 0 {
			nx, ny = -nx, -ny
		}
		bow := math.Min(l*.22, H*.3)
		cp := [2]float64{(p0[0]+p3[0])/2 + nx*bow, (p0[1]+p3[1])/2 + ny*bow}
		p1 := [2]float64{p0[0] + (cp[0]-p0[0])*2/3, p0[1] + (cp[1]-p0[1])*2/3}
		p2 := [2]float64{p3[0] + (cp[0]-p3[0])*2/3, p3[1] + (cp[1]-p3[1])*2/3}
		path := trimEnds(curve(p0, p1, p2, p3), 4.5)
		if a.always {
			// Where labels would hide an arc, whatever is selected.
			for _, p := range path {
				if cx, cy := int(p[0])/2, int(p[1])/4; cx >= 0 && cy >= 0 && cx < w && cy < mh {
					arcCells[cy*w+cx] = true
				}
			}
		}
		e := a.edge
		e.Kind = edgePath
		if a.declared {
			e.Kind = edgeLink
		}
		c, pri, dash := edgeStyle(e, mapFocus{})
		dash = arcDash(dash, m.GeoLabels != geoCards)
		if a.focused {
			c, pri = Mix(c, cGlow, .4), 8
		}
		d.Polyline(path, c, pri, dash)
		if a.forward || !a.backward {
			m.pulse(d, path, e, phase, false)
		}
		if a.backward {
			e.ID += "<"
			m.pulse(d, path, e, phase, true)
		}
	}
	// Halos: a quiet ring around every site; sites that need attention send
	// out a slow ripple so they are found before they are read.
	for k, s := range sites {
		c := signalColor(s.signal)
		ring := Mix(c, cInk, .62)
		if s.focused || s.onRoute {
			ring = Mix(c, cGlow, .25)
		}
		halo(d, s.x, s.y, 3.2, g.ky, ring, 7)
		if signalRank(s.signal) >= 3 {
			t := math.Mod(phase*.5+float64(k)*.37, 1)
			halo(d, s.x, s.y, 4.5+5.5*t, g.ky, Mix(c, cInk, .35+.6*t), 6)
		}
	}
	// Labels are placed before the dots are drawn, so their leaders are
	// dots too, under the arcs.
	labelTag := " " + m.GeoLabels.name() + " · p "
	zoomTag := " " + []string{"fit", "focus", "world"}[m.Zoom%3] + " · z "
	if width(labelTag)+width(zoomTag) > w-2 {
		labelTag = ""
	}
	if width(zoomTag) > w-2 {
		zoomTag = ellipsis(" z ", max(0, w-2))
	}
	tag := labelTag + zoomTag
	taken := make([]bool, w*mh)
	for i := max(0, w-width(tag)-2); i < w; i++ {
		taken[i] = true
	}
	// A fold's seam is a dotted line down the middle of a blank strip,
	// headed by how many degrees of longitude it holds, so it is never
	// taken for a coast or a meridian.
	type seam struct {
		x     int
		label string
	}
	seams := []seam{}
	for _, f := range g.folds {
		sx := (g.squeeze(f.a) + f.seam/2) * g.kx / g.scale
		for py := 0; py < int(H); py += 2 {
			d.Set(int(sx), py, Mix(cLine, cAccent, .2), 1)
		}
		label := " " + itoa(int(math.Round(f.b-f.a))) + "° folded "
		lx := int(sx)/2 - width(label)/2
		seams = append(seams, seam{lx, label})
		for i := max(0, lx-1); i < min(w, lx+width(label)+1); i++ {
			taken[i] = true
		}
	}
	// With the map to itself, a plate also says what runs on each node.
	// Locations keeps only compact place labels; Points leaves the markers.
	var runs map[string][]span
	var plates []plate
	if m.GeoLabels != geoPoints {
		if m.FullMap && m.GeoLabels == geoCards {
			runs = m.nodeServices()
		}
		var complete bool
		plates, complete = m.plates(sites, nodes, f, w, mh, m.cellAspect(), landCells, arcCells, taken, runs)
		if !complete {
			drawLayoutLimit(cv, x, y, w, h, len(nodes), len(edges))
			return
		}
		for _, p := range plates {
			if !p.beside {
				p.leader(d, Mix(signalColor(p.site.signal), cInk, .25), g.ky)
			}
		}
	}
	d.Blit(cv, x, y, 0)
	for _, s := range sites {
		cx, cy := int(s.x)/2, int(s.y)/4
		if cx < 0 || cy < 0 || cx >= w || cy >= mh {
			continue
		}
		lead := s.nodes[0]
		for _, i := range s.nodes {
			if f.hasNode(nodes[i].ID) {
				lead = i
				break
			}
		}
		cv.Set(x+cx, y+cy, signalGlyph(s.signal), fg(signalColor(s.signal)).bold())
		m.hit(x+cx, y+cy, 1, "select:node:"+nodes[lead].ID)
	}
	for _, s := range seams {
		if s.x >= 0 && s.x+width(s.label) < w-width(tag)-2 {
			cv.Text(x+s.x, y, s.label, fg(Mix(cLine, cAccent, .45)), -1)
		}
	}
	for _, p := range plates {
		m.drawPlate(cv, x, y, p, nodes, f, runs)
	}
	if tray {
		ty := y + mh
		cx := x + 1
		cx += cv.Text(cx, ty, "no location  ", fg(cFaint).italic(), -1)
		for _, i := range unplaced {
			n := nodes[i]
			if cx+width(n.Name)+3 >= x+w {
				break
			}
			st := fg(cMuted)
			if f.hasNode(n.ID) {
				st = fg(cGlow).Bg(cSelBG).bold()
			}
			sig := nodeSignal(n)
			cv.Set(cx, ty, signalGlyph(sig), fg(signalColor(sig)))
			k := cv.Text(cx+2, ty, n.Name, st, x+w-cx-2)
			m.hit(cx, ty, k+2, "select:node:"+n.ID)
			cx += k + 5
		}
	}
	if len(pts) == 0 {
		msg := "No node locations declared — add one with e (edit) or press m for the network view"
		cv.Text(x+max(0, (w-width(msg))/2), y+mh/2, msg, fg(cMuted).italic(), w)
	}
	tx := x + w - width(tag) - 1
	if labelTag != "" {
		cv.Text(tx, y, labelTag, fg(cMuted), -1)
		m.hit(tx, y, width(labelTag), "key:p")
	}
	tx += width(labelTag)
	cv.Text(tx, y, zoomTag, fg(cFaint), -1)
	m.hit(tx, y, width(zoomTag), "key:z")
}

// halo rings a point with dots r dot widths from it, round on screen when
// dots are ky times as tall as they are wide.
func halo(d plotter, cx, cy, r, ky float64, c Color, pri int8) {
	steps := max(12, int(r*7))
	for i := 0; i < steps; i++ {
		a := 2 * math.Pi * float64(i) / float64(steps)
		d.Set(int(math.Round(cx+math.Cos(a)*r)), int(math.Round(cy+math.Sin(a)*r/ky)), c, pri)
	}
}
