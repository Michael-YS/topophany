package tui

import (
	"math"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/view"
)

type mapNode = view.MapNode
type mapEdge = view.MapEdge

const (
	edgeLink    = view.EdgeLink
	edgePath    = view.EdgePath
	edgeContain = view.EdgeContain
	edgeVia     = view.EdgeVia // an edge or proxy in front of an endpoint's node
)

// What a map element may claim is its signal; see view.SignalRank.
func signalRank(s string) int        { return view.SignalRank(s) }
func worseSignal(a, b string) string { return view.WorseSignal(a, b) }

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

func nodeSignal(n mapNode) string { return view.NodeSignal(n) }

// graph derives drawable nodes and edges from the snapshot, and the hue of
// each node group. Path-implied edges are declared expectations (dashed),
// never presented as measured links.
func (m *Model) graph() ([]mapNode, []mapEdge, map[string]Color) {
	groups := map[string]Color{}
	for _, n := range m.Snapshot.Topology.Nodes {
		if _, ok := groups[n.Group]; !ok {
			groups[n.Group] = groupHues[len(groups)%len(groupHues)]
		}
	}
	nodes, edges := m.view().Graph()
	if m.NerdFont {
		// Every map writes a node's name from here, so each leads it with
		// its provider's logo.
		for i := range nodes {
			nodes[i].Name = m.logoText(nodes[i].Provider, nodes[i].Name)
		}
	}
	return nodes, edges, groups
}

// mapFocus is what the current selection lights up on a map: a node (shown
// by name), or the nodes and hops of a declared path, or a single link.
type mapFocus struct {
	node  string
	nodes map[string]bool
	edges map[string]bool
	hops  map[[2]string]bool
}

func (f mapFocus) any() bool { return f.node != "" || len(f.nodes) > 0 || len(f.edges) > 0 }
func (f mapFocus) hasNode(id string) bool {
	return id != "" && (id == f.node || f.nodes[id])
}
func (f mapFocus) hasEdge(e mapEdge) bool {
	return f.edges[e.ID] || f.hops[[2]string{e.From, e.To}] || f.hops[[2]string{e.To, e.From}] ||
		f.node != "" && (e.From == f.node || e.To == f.node)
}

// focus maps the selected row to what it means on the map. Services,
// observers and endpoints focus their node; paths and incidents on a path
// focus the whole declared route.
func (m *Model) focus(sel Row) mapFocus {
	t := m.Snapshot.Topology
	f := mapFocus{nodes: map[string]bool{}, edges: map[string]bool{}, hops: map[[2]string]bool{}}
	route := func(id string) {
		for _, p := range t.Paths {
			if p.ID != id {
				continue
			}
			for i, n := range p.Nodes {
				f.nodes[n] = true
				if i > 0 {
					f.hops[[2]string{p.Nodes[i-1], n}] = true
				}
			}
			for _, l := range p.Links {
				f.edges[l] = true
			}
		}
	}
	switch sel.Kind {
	case "node":
		f.node = sel.ID
	case "link":
		f.edges[sel.ID] = true
	case "path":
		route(sel.ID)
	case "endpoint":
		for _, e := range t.Endpoints {
			if e.ID == sel.ID {
				f.node = e.Node
			}
		}
	case "service":
		for _, v := range t.Services {
			if v.ID == sel.ID {
				f.node = v.Node
			}
		}
	case "observer":
		for _, o := range t.Observers {
			if o.ID == sel.ID {
				f.node = o.Node
			}
		}
	case "check":
		for _, c := range t.Checks {
			if c.ID != sel.ID {
				continue
			}
			route(c.Path)
			for _, e := range t.Endpoints {
				if e.ID == c.Endpoint && c.Path == "" {
					f.node = e.Node
				}
			}
		}
	case "incident":
		if _, scope, ok := strings.Cut(sel.ID, "/"); ok {
			scope, _, _ = strings.Cut(scope, "#")
			parts := strings.Split(scope, "/")
			if len(parts) >= 3 && parts[1] == "path" {
				route(parts[2])
			} else if len(parts) >= 3 && parts[1] == "asset" {
				f.node = parts[2]
			}
		}
	}
	return f
}

// mapModes are the map's views in the order m cycles through them.
var mapModes = []string{"virtual", "geo", "network"}

// mapMode is the view the map shows: the one chosen with m, otherwise the
// virtual geo view when any node has a location and the network graph when
// none has.
func (m *Model) mapMode() string {
	if contains(mapModes, m.MapMode) {
		return m.MapMode
	}
	for _, n := range m.Snapshot.Topology.Nodes {
		if n.Location != nil {
			return "virtual"
		}
	}
	return "network"
}

// mapRows is how many rows a w-cell-wide map needs, legend and tray
// included, in a mode with a natural height; 0 in one that fills any height.
// Layouts give the rest of a tall panel to other panels.
func (m *Model) mapRows(w int) int {
	switch m.mapMode() {
	case "geo":
		return m.geoRows(w) + m.mapChrome()
	case "virtual":
		if rows := m.virtualRows(w); rows > 0 {
			return rows + 1
		}
	}
	return 0
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
	f := m.focus(sel)
	legend := h >= 6
	mh := h
	if legend {
		mh--
	}
	m.drawMapBody(cv, x, y, w, mh, nodes, edges, groups, f)
	if legend {
		m.mapLegend(cv, x, y+h-1, w, nodes, edges, groups)
	}
}

// drawMapBody draws the map the mode shows, without its legend.
func (m *Model) drawMapBody(cv *Canvas, x, y, w, h int, nodes []mapNode, edges []mapEdge, groups map[string]Color, f mapFocus) {
	switch m.mapMode() {
	case "geo":
		m.drawGeo(cv, x, y, w, h, nodes, edges, f)
	case "virtual":
		m.drawVirtual(cv, x, y, w, h, nodes, edges, groups, f)
	default:
		m.drawNetwork(cv, x, y, w, h, nodes, edges, groups, f)
	}
}

// mapLegend says along row ly what the map's marks and lines mean, as
// much as fits in w.
func (m *Model) mapLegend(cv *Canvas, x, ly, w int, nodes []mapNode, edges []mapEdge, groups map[string]Color) {
	cx := x + 1
	item := func(r rune, c Color, label string) {
		if cx+3+width(label) >= x+w {
			return
		}
		cv.Set(cx, ly, r, fg(c))
		cx += 2 + cv.Text(cx+2, ly, label, fg(cMuted), -1) + 2
	}
	if m.mapMode() == "virtual" && m.vcache.dropped > 0 {
		// Links the virtual map found no room for are said, not hidden.
		item('⋯', cWarn, plural(m.vcache.dropped, "link")+" not drawn · m")
		cx += 2
	}
	item('●', cUp, "up")
	item('▲', cWarn, "warn")
	item('✖', cDown, "down")
	item('○', cUnknown, "unknown")
	cx += 2
	line := '━'
	if m.mapMode() == "geo" && m.GeoLabels != geoCards {
		line = '┄'
	}
	item(line, Mix(cUp, cInk, .45), "path ok")
	item(line, cWarn, "affected")
	item(line, cDown, "failed")
	item('┄', Mix(cUnknown, cInk, .35), "no evidence")
	for _, e := range edges {
		if e.Kind == edgeVia {
			item('┈', Mix(cUp, cInk, .45), "via edge")
			break
		}
	}
	if m.mapMode() == "network" {
		item('┊', cFaint, "hosted in")
	}
	if m.mapMode() != "geo" {
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

func edgeStyle(e mapEdge, f mapFocus) (Color, int8, int) {
	var c Color
	pri, dash := int8(3), 0
	// Healthy links are quiet threads and links nobody measured fainter
	// still, so the eye goes to the problems, drawn at full strength.
	switch e.Signal {
	case "up", "carried":
		c, pri = Mix(cUp, cInk, .64), 4
	case "affected", "degraded":
		c, pri = cWarn, 5
	case "down":
		c, pri = cDown, 6
	default:
		c = Mix(cUnknown, cInk, .5)
	}
	switch e.Kind {
	case edgePath:
		dash = 2
		if e.Signal == "unknown" || e.Signal == "carried" {
			c, pri = Mix(c, cInk, .3), pri-1
		}
	case edgeVia:
		dash = 1
	case edgeContain:
		c, pri, dash = Mix(cFaint, cInk, .2), 2, 1
	}
	if f.hasEdge(e) {
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
			d.Set(int(math.Round(p[0])), int(math.Round(p[1])), Mix(Mix(cUp, cGlow, .3), Mix(cUp, cInk, .5), float64(k)/5), 9)
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

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
