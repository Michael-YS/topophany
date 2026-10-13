package tui

import (
	"math"
	"strings"

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
	f := m.focus(sel)
	legend := h >= 6
	mh := h
	if legend {
		mh--
	}
	if m.mapMode() == "geo" {
		m.drawGeo(cv, x, y, w, mh, nodes, edges, f)
	} else {
		m.drawNetwork(cv, x, y, w, mh, nodes, edges, groups, f)
	}
	if legend {
		ly := y + h - 1
		cx := x + 1
		item := func(r rune, c Color, label string) {
			if cx+3+width(label) >= x+w {
				return
			}
			cv.Set(cx, ly, r, fg(c))
			cx += 2 + cv.Text(cx+2, ly, label, fg(cMuted), -1) + 2
		}
		item('●', cUp, "up")
		item('▲', cWarn, "warn")
		item('✖', cDown, "down")
		item('○', cUnknown, "unknown")
		cx += 2
		item('━', Mix(cUp, cInk, .45), "path ok")
		item('━', cWarn, "affected")
		item('━', cDown, "failed")
		item('┄', Mix(cUnknown, cInk, .35), "no evidence")
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
