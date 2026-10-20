package view

import "github.com/WhitenWhiten/topophany/internal/model"

// The maps draw the topology as nodes and edges. Every edge says only what
// the evidence lets it: declared links carry their own checks, and hops
// implied by a declared path or by the edges in front of an endpoint are
// expectations, never measured links.

// MapNode is a node as the maps place it. Located is true when it has a
// declared location, its own or, when Inherited, its parent's. A colo
// pseudo-node (Kind "colo") is not declared in the topology: it is placed by
// a fresh check's reported edge_pop, from Cloudflare's location list.
type MapNode struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind,omitempty"`
	Group     string  `json:"group,omitempty"`
	Health    string  `json:"health"`
	Place     string  `json:"place,omitempty"`
	Parent    string  `json:"parent,omitempty"`
	Provider  string  `json:"provider,omitempty"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Located   bool    `json:"located"`
	Inherited bool    `json:"inherited"`
	Affected  bool    `json:"affected"`
}

// MapEdge is a line between two nodes; Signal is what it may claim (see
// SignalRank) and Observer, for a hop implied by a path, whose path it is.
type MapEdge struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Health   string `json:"health"`
	Label    string `json:"label,omitempty"`
	Kind     int    `json:"-"`
	Signal   string `json:"signal"`
	Observer string `json:"observer,omitempty"`
}

// The kinds of edge.
const (
	EdgeLink    = iota // a declared link
	EdgePath           // a hop implied by a declared path
	EdgeContain        // a node hosted in another
	EdgeVia            // an edge or proxy in front of an endpoint's node
	EdgeColo           // a reported colo between an observer and the edge it reached
)

// EdgeKinds names them.
var EdgeKinds = []string{"link", "path", "contains", "via", "colo"}

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
func SignalRank(s string) int {
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

// WorseSignal keeps the signal that needs more attention; between equally
// ranked signals a measured "up" beats an inferred "carried".
func WorseSignal(a, b string) string {
	if SignalRank(b) > SignalRank(a) || SignalRank(b) == SignalRank(a) && b == "up" {
		return b
	}
	return a
}

// NodeSignal is what a node may claim: affected when it is on a failing
// path and its own evidence shows no failure.
func NodeSignal(n MapNode) string {
	if n.Affected && (n.Health == "up" || n.Health == "unknown") {
		return "affected"
	}
	return n.Health
}

// Graph derives the map's nodes and edges. Path-implied edges are declared
// expectations, never presented as measured links.
func (v View) Graph() ([]MapNode, []MapEdge) {
	s := v.Snapshot
	cached := v.Cached
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
	nodes := []MapNode{}
	index := map[string]int{}
	for _, n := range s.Topology.Nodes {
		name := n.Name
		if name == "" {
			name = n.ID
		}
		h, affected := health(nodeStates, n.ID)
		mn := MapNode{ID: n.ID, Name: name, Kind: n.Kind, Group: n.Group, Parent: n.Parent, Provider: n.Provider, Health: h, Affected: affected}
		if n.Location != nil {
			mn.Lat, mn.Lon, mn.Located, mn.Place = n.Location.Lat, n.Location.Lon, true, n.Location.Label
		}
		index[n.ID] = len(nodes)
		nodes = append(nodes, mn)
	}
	// Children without their own placement share the parent's declared site.
	placement := make([]uint8, len(nodes))
	var locate func(int) bool
	locate = func(i int) bool {
		if nodes[i].Located {
			return true
		}
		if placement[i] != 0 { // drafts may contain a cycle or a missing site
			return false
		}
		placement[i] = 1
		if p, ok := index[nodes[i].Parent]; ok && locate(p) {
			nodes[i].Lat, nodes[i].Lon, nodes[i].Place = nodes[p].Lat, nodes[p].Lon, nodes[p].Place
			nodes[i].Located, nodes[i].Inherited = true, true
		}
		placement[i] = 2
		return nodes[i].Located
	}
	for i := range nodes {
		locate(i)
	}
	// The colo an edge reported answering from sits at its place, between
	// the observer that saw it and the edge it fronts. Its nodes are added
	// before the other edges are derived; its own two hops come last, so
	// they cannot change how a declared link or path is drawn.
	coloNodes, coloEdges := v.coloNodes(index)
	for _, n := range coloNodes {
		index[n.ID] = len(nodes)
		nodes = append(nodes, n)
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
	edges := []MapEdge{}
	seen := map[[2]string]bool{}
	pathEdges := map[[2]string]int{}
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
		edges = append(edges, MapEdge{ID: l.ID, From: l.From, To: l.To, Health: h, Label: l.Transport, Kind: EdgeLink, Signal: sig})
		seen[[2]string{l.From, l.To}] = true
		seen[[2]string{l.To, l.From}] = true
	}
	// An endpoint reached through edges or proxies implies hops from each
	// to the next and on to its node, unless a link already declares them.
	// The checks on the endpoint color them: carried when one passes,
	// affected when one fails and none pass.
	target := map[string]string{}
	for _, x := range s.Topology.Checks {
		target[x.ID] = x.Endpoint
	}
	checkHealth := map[string][]string{}
	for _, cs := range s.Checks {
		if ep := target[cs.ID]; ep != "" && !cached {
			checkHealth[ep] = append(checkHealth[ep], cs.Health)
		}
	}
	viaEdges := map[[2]string]int{}
	for _, ep := range s.Topology.Endpoints {
		hops := append(append([]string{}, ep.Via...), ep.Node)
		sig := "unknown"
		for _, h := range checkHealth[ep.ID] {
			switch {
			case h == "up":
				sig = "carried"
			case (h == "down" || h == "degraded") && sig == "unknown":
				sig = "affected"
			}
		}
		for i := 1; i < len(hops) && len(ep.Via) > 0; i++ {
			a, b := hops[i-1], hops[i]
			_, okA := index[a]
			_, okB := index[b]
			if k, ok := viaEdges[[2]string{a, b}]; ok {
				// Endpoints sharing a hop: keep what needs attention.
				edges[k].Signal = WorseSignal(edges[k].Signal, sig)
				continue
			}
			if !okA || !okB || seen[[2]string{a, b}] {
				continue
			}
			seen[[2]string{a, b}], seen[[2]string{b, a}] = true, true
			viaEdges[[2]string{a, b}] = len(edges)
			edges = append(edges, MapEdge{ID: "via:" + ep.ID, From: a, To: b, Health: "unknown", Label: "in front of " + ep.ID, Kind: EdgeVia, Signal: sig})
		}
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
			key := [2]string{a, b}
			if key[0] > key[1] {
				key[0], key[1] = key[1], key[0]
			}
			if k, ok := pathEdges[key]; ok {
				e := &edges[k]
				if WorseSignal(e.Signal, sig) != e.Signal {
					e.Signal, e.Health, e.ID, e.Observer = sig, h, "path:"+p.ID, p.Observer
				}
			} else {
				pathEdges[key] = len(edges)
				edges = append(edges, MapEdge{ID: "path:" + p.ID, From: a, To: b, Health: h, Label: "declared in path " + p.ID, Kind: EdgePath, Signal: sig, Observer: p.Observer})
			}
		}
	}
	for _, n := range s.Topology.Nodes {
		if _, ok := index[n.Parent]; ok && n.Parent != "" {
			edges = append(edges, MapEdge{ID: "parent:" + n.ID, From: n.Parent, To: n.ID, Health: "unknown", Kind: EdgeContain, Signal: "unknown"})
		}
	}
	edges = append(edges, coloEdges...)
	return nodes, edges
}

// MaxColoNodes bounds the colo pseudo-nodes one map draws, so a topology
// that reports many colos stays inside the map's structural budget. The
// checks and servers views still name every colo; only the optional diagram
// stops.
const MaxColoNodes = 32

// coloCheckSignal is what a check's health lets a reported colo claim: the
// request carried over it when the check passed, and only that the route was
// affected when it failed, because a failing request does not establish that
// the colo itself is at fault.
func coloCheckSignal(health string) string {
	switch health {
	case "up":
		return "carried"
	case "down", "degraded":
		return "affected"
	}
	return "unknown"
}

// coloNodes derives the colo pseudo-nodes of a map from the fresh checks that
// saw an edge name a colo. One node is drawn per (edge, code), however many
// observers and checks reported it, at the colo's coordinates when Cloudflare's
// generated list knows the code and unplaced otherwise; each node carries one
// hop from every observer node that saw it and one to the edge it reached. A
// stale, paused, cached or withheld sample contributes nothing: the label is a
// report, and only a fresh one places it. Graph indexes and appends what this
// returns; the index is consulted only for nodes that must already exist.
func (v View) coloNodes(index map[string]int) ([]MapNode, []MapEdge) {
	if v.Cached || v.Draft {
		return nil, nil
	}
	s := v.Snapshot
	endpoints := map[string]model.Endpoint{}
	for _, ep := range s.Topology.Endpoints {
		endpoints[ep.ID] = ep
	}
	observers := map[string]string{}
	for _, o := range s.Topology.Observers {
		if o.Node != "" {
			observers[o.ID] = o.Node
		}
	}
	nodes := map[string]model.Node{}
	for _, n := range s.Topology.Nodes {
		nodes[n.ID] = n
	}
	states := map[string]model.CheckState{}
	for _, cs := range s.Checks {
		states[cs.ID] = cs
	}
	type placement struct {
		node      MapNode
		code      string
		edge      string
		edgeName  string
		signal    string
		observers []string
		seen      map[string]bool
	}
	order := []string{}
	byKey := map[string]*placement{}
	observerSignal := map[[2]string]string{}
	for _, x := range s.Topology.Checks {
		cs, ok := states[x.ID]
		if !ok || cs.Freshness != "fresh" || cs.Last == nil {
			continue
		}
		facts := cs.Last.Facts
		code := facts["edge_pop"]
		// Vercel's lowercase region is not a colo, a malformed token is not
		// a place, and a sample that withheld its labels must stay
		// withheld: a worker that leaked a code anyway does not place it.
		if code == "" || facts["peer_text"] == "withheld_secret_request" ||
			facts["edge_provider"] == "vercel" || !model.IsColo(code) {
			continue
		}
		ep, ok := endpoints[x.Endpoint]
		if !ok {
			continue
		}
		// The edge pill is the first hop in front of the endpoint, or the
		// endpoint's own node when that node is itself an edge (a Worker or a
		// Pages site). Anything behind a proxy is not drawn as a colo hop.
		edge := ep.Node
		if len(ep.Via) > 0 {
			edge = ep.Via[0]
		}
		en, ok := nodes[edge]
		if !ok || en.Kind != "edge" {
			continue
		}
		obs, ok := observers[x.Observer]
		if !ok {
			continue
		}
		if _, ok := index[obs]; !ok {
			continue
		}
		key := edge + "\x00" + code
		p := byKey[key]
		if p == nil {
			if len(order) == MaxColoNodes {
				continue
			}
			name := en.Name
			if name == "" {
				name = en.ID
			}
			p = &placement{code: code, edge: edge, edgeName: name, seen: map[string]bool{}, node: MapNode{
				ID:       "colo:" + edge + ":" + code,
				Name:     code,
				Kind:     "colo",
				Group:    en.Group,
				Provider: en.Provider,
				Health:   "unknown",
			}}
			if c, known := ColoAt(code); known {
				place := c.City
				if place == "" {
					place = code
				}
				p.node.Lat, p.node.Lon, p.node.Located, p.node.Place = c.Lat, c.Lon, true, place
			}
			byKey[key] = p
			order = append(order, key)
		}
		sig := coloCheckSignal(cs.Health)
		p.signal = WorseSignal(p.signal, sig)
		if !p.seen[obs] {
			p.seen[obs] = true
			p.observers = append(p.observers, obs)
		}
		k := [2]string{key, obs}
		observerSignal[k] = WorseSignal(observerSignal[k], sig)
	}
	if len(order) == 0 {
		return nil, nil
	}
	out := make([]MapNode, 0, len(order))
	hops := make([]MapEdge, 0, 2*len(order))
	for _, key := range order {
		p := byKey[key]
		if p.signal == "carried" {
			p.node.Health = "up"
		}
		if SignalRank(p.signal) >= SignalRank("affected") {
			p.node.Affected = true
		}
		out = append(out, p.node)
		for _, obs := range p.observers {
			hops = append(hops, MapEdge{
				ID:     "colo:" + p.edge + ":" + p.code + ":from:" + obs,
				From:   obs,
				To:     p.node.ID,
				Health: "unknown",
				Label:  "answered from " + p.code,
				Kind:   EdgeColo,
				Signal: observerSignal[[2]string{key, obs}],
			})
		}
		hops = append(hops, MapEdge{
			ID:     "colo:" + p.edge + ":" + p.code + ":to",
			From:   p.node.ID,
			To:     p.edge,
			Health: "unknown",
			Label:  "in front of " + p.edgeName,
			Kind:   EdgeColo,
			Signal: p.signal,
		})
	}
	return out, hops
}
