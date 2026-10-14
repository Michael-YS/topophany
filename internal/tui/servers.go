package tui

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// The Servers tab: a card for each server, laid out as a wall of columns.
// A card says what the server is and where, what runs on it and how it is
// reached, what it hosts and who observes from it, and ends with the
// operator's notes as a memo. Edges and platforms get cards too, saying
// what runs there, what they stand in front of and where they answered.

const (
	cardMinW = 42 // narrower than this, the wall drops a column
	cardMaxW = 64
)

// serverNode reports whether a node gets a card of its own: anything but an
// external party or a container, which appears on its host's card.
func serverNode(n model.Node, known map[string]bool) bool {
	return n.Kind != "external" && !(n.Kind == "container" && known[n.Parent])
}

// cardRow is one line inside a card: spans from the left, spans pushed to
// the right edge, an optional tint and a click target.
type cardRow struct {
	left, right []span
	bg          Color
	bar         Color // a stripe at the card's inner left edge
	hit         string
}

// fleet is what the cards are built from, indexed once per frame.
type fleet struct {
	names     map[string]string
	nodes     map[string]model.Node
	nodeState map[string]model.EntityState
	services  map[string][]model.Service // by node
	svcState  map[string]model.EntityState
	endpoints map[string][]model.Endpoint // by node
	fronted   map[string][]model.Endpoint // by the edge or proxy in front
	epHealth  map[string]string
	children  map[string][]model.Node
	observers map[string][]model.Observer // by node
	obsState  map[string]model.ObserverState
	links     map[string][]model.Link // by either end
	pops      map[string][]string     // where each edge answered
	groups    map[string]Color
}

func (m *Model) fleet() *fleet {
	s := m.Snapshot
	t := s.Topology
	f := &fleet{names: map[string]string{}, nodes: map[string]model.Node{}, nodeState: map[string]model.EntityState{}, services: map[string][]model.Service{}, svcState: map[string]model.EntityState{}, endpoints: map[string][]model.Endpoint{}, fronted: map[string][]model.Endpoint{}, epHealth: map[string]string{}, children: map[string][]model.Node{}, observers: map[string][]model.Observer{}, obsState: map[string]model.ObserverState{}, links: map[string][]model.Link{}, pops: map[string][]string{}, groups: m.groupColors()}
	cached := m.Cached()
	for _, n := range t.Nodes {
		f.nodes[n.ID] = n
		f.names[n.ID] = n.Name
		if n.Name == "" {
			f.names[n.ID] = n.ID
		}
		if n.Parent != "" && n.Parent != n.ID {
			f.children[n.Parent] = append(f.children[n.Parent], n)
		}
	}
	for _, e := range s.Nodes {
		f.nodeState[e.ID] = e
	}
	for _, v := range t.Services {
		f.services[v.Node] = append(f.services[v.Node], v)
	}
	for _, e := range s.Services {
		f.svcState[e.ID] = e
	}
	endpoints := map[string]model.Endpoint{}
	for _, ep := range t.Endpoints {
		endpoints[ep.ID] = ep
		f.endpoints[ep.Node] = append(f.endpoints[ep.Node], ep)
		for _, v := range ep.Via {
			f.fronted[v] = append(f.fronted[v], ep)
		}
	}
	for _, o := range t.Observers {
		f.observers[o.Node] = append(f.observers[o.Node], o)
	}
	for _, o := range s.Observers {
		f.obsState[o.ID] = o
	}
	for _, l := range t.Links {
		f.links[l.From] = append(f.links[l.From], l)
		if l.To != l.From {
			f.links[l.To] = append(f.links[l.To], l)
		}
	}
	target := map[string]string{}
	for _, x := range t.Checks {
		target[x.ID] = x.Endpoint
	}
	health := map[string][]string{}
	for _, cs := range s.Checks {
		ep, ok := endpoints[target[cs.ID]]
		if !ok {
			continue
		}
		health[ep.ID] = append(health[ep.ID], cs.Health)
		// Where an edge said it answered from, freshly.
		if cs.Freshness != "fresh" || cs.Last == nil || cached {
			continue
		}
		at := ep.Node
		if len(ep.Via) > 0 {
			at = ep.Via[0]
		}
		if pop := cs.Last.Facts["edge_pop"]; pop != "" && !contains(f.pops[at], pop) {
			f.pops[at] = append(f.pops[at], pop)
		}
	}
	for id, hs := range health {
		f.epHealth[id] = aggregateHealth(hs)
		if cached {
			f.epHealth[id] = "unknown"
		}
	}
	for at := range f.pops {
		sort.Strings(f.pops[at])
	}
	return f
}

// netColors tint the network of an address so a card shows at a glance
// what is public and what only travels the tailnet or the LAN.
var netColors = map[string]Color{"public": cAccent2, "tailnet": cEdit, "lan": Hex(0x86EFAC), "loopback": cMuted, "external": cFaint}

// card builds the rows of a server's card for content width w.
func (m *Model) card(f *fleet, r Row, w int, selected bool) []cardRow {
	n := f.nodes[r.ID]
	cached := m.Cached()
	hue := f.groups[n.Group]
	if hosted(n.Kind) {
		hue = providerColor(n.Provider, hue)
	}
	band := Mix(hue, cInk, .84)
	rows := []cardRow{}
	add := func(cr cardRow) { rows = append(rows, cr) }
	// Long lists keep their first entries; Enter shows the rest.
	const most = 8
	more := func(n int) bool {
		if n > most {
			add(cardRow{left: []span{{"+" + itoa(n-most+1) + " more", fg(cFaint)}}})
			return true
		}
		return false
	}
	heading := func(title string, count int) {
		if len(rows) > 0 {
			add(cardRow{})
		}
		head := []span{{title, fg(Mix(cAccent, cMuted, .35)).bold()}}
		if count > 1 {
			head = append(head, span{" · " + itoa(count), fg(cFaint)})
		}
		add(cardRow{left: head})
	}
	state := func(h string) string {
		if cached {
			return "unknown"
		}
		return h
	}

	// The header: what it is, its state, its kind and where it is.
	pill, pillStyle := statePill(r)
	name := fg(cText).bold()
	if selected {
		name = fg(cGlow).bold()
	}
	glyph, gc := healthGlyph(r.Health), healthColor(r.Health)
	if r.Note == "affected" {
		glyph, gc = '▲', cWarn
	}
	add(cardRow{left: []span{{string(glyph) + " ", fg(gc).bold()}, {r.Title, name}}, right: []span{{pill, pillStyle}}, bg: band, bar: hue})
	kind := kindLabel(n)
	if kind == "" {
		kind = "node"
	}
	sub := []span{{kind, fg(Mix(hue, cText, .35))}}
	if n.Group != "" {
		sub = append(sub, span{" · " + n.Group, fg(cMuted)})
	}
	where := []span{}
	switch {
	case n.Location != nil && n.Location.Label != "":
		where = []span{{"◎ " + n.Location.Label, fg(cMuted)}}
	case n.Location != nil:
		where = []span{{"◎ " + latLon(*n.Location), fg(cMuted)}}
	case n.Kind == "edge":
		where = []span{{"◌ anycast", fg(cMuted)}}
	case n.Parent != "":
		where = []span{{"on " + f.names[n.Parent], fg(cMuted)}}
	}
	add(cardRow{left: sub, right: where, bg: band, bar: hue})

	// What runs here, healthiest last so trouble reads first.
	services := append([]model.Service(nil), f.services[n.ID]...)
	rank := func(v model.Service) int {
		e := f.svcState[v.ID]
		if cached {
			return serviceRank(Row{Health: "unknown"})
		}
		return serviceRank(Row{Health: e.Health, Note: map[bool]string{true: "affected"}[e.Affected && e.Health != "down"]})
	}
	sort.SliceStable(services, func(i, j int) bool { return rank(services[i]) < rank(services[j]) })
	if len(services) > 0 {
		title := "SERVICES"
		if hosted(n.Kind) {
			title = "RUNS HERE"
		}
		heading(title, len(services))
		for i, v := range services {
			if i == most-1 && more(len(services)) {
				break
			}
			e := f.svcState[v.ID]
			h := state(e.Health)
			g, c := healthGlyph(h), healthColor(h)
			if e.Affected && h != "down" && !cached {
				g, c = '▲', cWarn
			}
			right := []span{}
			switch front := viaNodes(m.Snapshot.Topology, v.Endpoints); {
			case len(front) > 0:
				names := []string{}
				for _, id := range front {
					names = append(names, f.names[id])
				}
				right = append(right, span{"via " + strings.Join(names, " ⇢ "), fg(providerColor(f.nodes[front[0]].Provider, cMuted))})
			case v.Runtime != "":
				right = append(right, span{v.Runtime, fg(Mix(hue, cText, .2))})
			case len(e.Checks) == 0:
				right = append(right, span{"not monitored", fg(cFaint)})
			}
			add(cardRow{left: []span{{string(g) + " ", fg(c)}, {v.Name, fg(cText)}}, right: right, hit: "goto:service:" + v.ID})
		}
	}

	// An edge: the addresses it answers for, and where it answered from.
	if fronted := f.fronted[n.ID]; len(fronted) > 0 {
		heading("IN FRONT OF", len(fronted))
		for i, ep := range fronted {
			if i == most-1 && more(len(fronted)) {
				break
			}
			h := state(f.epHealth[ep.ID])
			add(cardRow{left: []span{{string(healthGlyph(h)) + " ", fg(healthColor(h))}, {hostOf(ep.Address), fg(cText)}}, right: []span{{"→ " + f.names[ep.Node], fg(cMuted)}}, hit: "select:endpoint:" + ep.ID})
		}
	}
	if pops := f.pops[n.ID]; len(pops) > 0 {
		heading("ANSWERED FROM", 0)
		add(cardRow{left: []span{{strings.Join(pops, " · "), fg(Mix(hue, cText, .25))}}, right: []span{{"response headers", fg(cFaint)}}})
	}

	// How it is reached.
	if eps := f.endpoints[n.ID]; len(eps) > 0 {
		heading("ADDRESSES", len(eps))
		for i, ep := range eps {
			if i == most-1 && more(len(eps)) {
				break
			}
			h := state(f.epHealth[ep.ID])
			if _, measured := f.epHealth[ep.ID]; !measured {
				h = ""
			}
			right := []span{}
			if len(ep.Via) > 0 {
				right = append(right, span{"⇠ " + f.names[ep.Via[0]] + " ", fg(providerColor(f.nodes[ep.Via[0]].Provider, cMuted))})
			}
			if h == "" {
				right = append(right, span{"·", fg(cFaint)})
			} else {
				right = append(right, span{string(healthGlyph(h)), fg(healthColor(h))})
			}
			net := ep.Network
			nc, ok := netColors[net]
			if !ok {
				nc = cMuted
			}
			add(cardRow{left: []span{{fit(net, 9), fg(nc)}, {ep.Address, fg(cText)}}, right: right, hit: "select:endpoint:" + ep.ID})
		}
	}

	// What it hosts.
	if kids := f.children[n.ID]; len(kids) > 0 {
		heading("HOSTS", len(kids))
		for i, k := range kids {
			if i == most-1 && more(len(kids)) {
				break
			}
			e := f.nodeState[k.ID]
			h := state(e.Health)
			tree := "├ "
			if i == len(kids)-1 {
				tree = "└ "
			}
			runs := []string{}
			for _, v := range f.services[k.ID] {
				runs = append(runs, v.Name)
			}
			add(cardRow{left: []span{{tree, fg(cFaint)}, {string(healthGlyph(h)) + " ", fg(healthColor(h))}, {f.names[k.ID], fg(cText)}}, right: []span{{strings.Join(runs, ", "), fg(cMuted)}}, hit: "select:node:" + k.ID})
		}
	}

	// Who observes from here.
	if obs := f.observers[n.ID]; len(obs) > 0 {
		heading("OBSERVES FROM HERE", len(obs))
		for _, o := range obs {
			st := f.obsState[o.ID]
			h := state(st.Health)
			add(cardRow{left: []span{{"◎ ", fg(healthColor(h))}, {o.Name, fg(cText)}}, right: []span{{age(m.Now, st.LastSeen), fg(cFaint)}}, hit: "goto:observer:" + o.ID})
		}
	}

	// Its declared connections.
	if links := f.links[n.ID]; len(links) > 0 {
		heading("LINKS", len(links))
		for i, l := range links {
			if i == 4 && len(links) > 5 {
				add(cardRow{left: []span{{"+" + itoa(len(links)-4) + " more", fg(cFaint)}}})
				break
			}
			arrow, other := "→ ", l.To
			if l.To == n.ID && l.From != n.ID {
				arrow, other = "← ", l.From
			}
			add(cardRow{left: []span{{arrow, fg(cAccent)}, {f.names[other], fg(cText)}}, right: []span{{l.Transport, fg(cFaint)}}, hit: "select:link:" + l.ID})
		}
	}

	// The operator's notes, as a memo.
	if memo := strings.TrimSpace(n.Notes); memo != "" {
		add(cardRow{})
		paper := Mix(cInk, cCity, .07)
		for i, line := range memoLines(memo, w-2) {
			lead := "  "
			if i == 0 {
				lead = "✎ "
			}
			add(cardRow{left: []span{{lead, fg(Mix(cCity, cInk, .2))}, {line, fg(Mix(cText, cCity, .18)).italic()}}, bg: paper, bar: Mix(cCity, cInk, .35)})
		}
	}
	return rows
}

// latLon writes a place as "35.7°N 139.7°E".
func latLon(l model.Location) string {
	ns, ew := "N", "E"
	if l.Lat < 0 {
		ns = "S"
	}
	if l.Lon < 0 {
		ew = "W"
	}
	return strconv.FormatFloat(math.Abs(l.Lat), 'f', 1, 64) + "°" + ns + " " + strconv.FormatFloat(math.Abs(l.Lon), 'f', 1, 64) + "°" + ew
}

// memoLines keeps the line breaks an operator wrote and wraps the rest.
func memoLines(s string, w int) []string {
	out := []string{}
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		para = strings.TrimSpace(secure.Text(para))
		if para == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrap(para, max(1, w))...)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// hostOf shortens an address to its host, which is what an edge answers
// for: "https://www.example.invalid/x" → "www.example.invalid".
func hostOf(addr string) string {
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if i := strings.IndexAny(addr, "/?#"); i >= 0 {
		addr = addr[:i]
	}
	return addr
}

// drawServers lays the cards out as a wall: as many columns as fit, each
// card dropped into the shortest column, scrolled so the selection shows.
func (m *Model) drawServers(cv *Canvas, x, y, w, h int, rows []Row) {
	if h <= 0 || w <= 0 {
		return
	}
	if len(rows) == 0 {
		msg := "No servers declared."
		switch {
		case m.Search != "":
			msg = "No matching servers."
		case m.Edit != nil:
			msg = "No servers yet — press a to add a node."
		case m.Loading != "" && len(m.Snapshot.Topology.Nodes) == 0:
			msg = "Waiting for the topology…"
		}
		cv.Text(x, y, msg, fg(cMuted).italic(), w)
		return
	}
	const gap = 2
	cols := max(1, min(len(rows), (w+gap)/(cardMinW+gap)))
	cw := min(cardMaxW, (w-gap*(cols-1))/cols)
	// Cards narrower than the room left over sit centred.
	border := x + w + 1
	x += (w - (cw*cols + gap*(cols-1))) / 2
	f := m.fleet()
	type placed struct{ col, top, h int }
	pos := make([]placed, len(rows))
	cards := make([][]cardRow, len(rows))
	heights := make([]int, cols)
	for i, r := range rows {
		cards[i] = m.card(f, r, cw-4, i == m.Selected)
		c := 0
		for k := range heights {
			if heights[k] < heights[c] {
				c = k
			}
		}
		pos[i] = placed{c, heights[c], len(cards[i]) + 2}
		heights[c] += len(cards[i]) + 3
	}
	total := 0
	for _, hh := range heights {
		total = max(total, hh-1)
	}
	if sel := pos[min(max(m.Selected, 0), len(pos)-1)]; sel.top < m.Offset {
		m.Offset = sel.top
	} else if sel.top+sel.h > m.Offset+h {
		m.Offset = min(sel.top, sel.top+sel.h-h)
	}
	m.Offset = min(max(m.Offset, 0), max(0, total-h))
	for i, r := range rows {
		p := pos[i]
		if p.top+p.h <= m.Offset || p.top >= m.Offset+h {
			continue
		}
		m.drawCard(cv, x+p.col*(cw+gap), y+p.top-m.Offset, cw, cards[i], r, i, y, y+h)
	}
	if total > h {
		// Where the window sits on the wall, on the panel's right border.
		thumb := max(1, h*h/total)
		top := (h - thumb) * m.Offset / max(1, total-h)
		for k := 0; k < thumb; k++ {
			cv.Set(border, y+top+k, '┃', fg(Mix(cLine, cAccent, .45)))
		}
	}
}

// drawCard draws one card with its top-left corner at (x, y), clipped to
// the rows from clipTop up to clipBottom.
func (m *Model) drawCard(cv *Canvas, x, y, w int, rows []cardRow, r Row, index int, clipTop, clipBottom int) {
	if w < 8 {
		return
	}
	selected := index == m.Selected
	paper := Mix(cInk, cPanelBG, .9)
	border := fg(cLine)
	switch {
	case selected:
		paper = Mix(cPanelBG, cSelBG, .45)
		border = fg(Mix(cLine, cAccent, .75))
	case r.Health == "down":
		border = fg(Mix(cLine, cDown, .55))
	case r.Health == "degraded" || r.Note == "affected":
		border = fg(Mix(cLine, cWarn, .45))
	}
	// Without color the selection is the heavier frame.
	frame := [6]rune{'╭', '╮', '╰', '╯', '─', '│'}
	if selected && m.NoColor {
		frame = [6]rune{'┏', '┓', '┗', '┛', '━', '┃'}
	}
	visible := func(yy int) bool { return yy >= clipTop && yy < clipBottom }
	for i := 0; i < len(rows)+2; i++ {
		yy := y + i
		if !visible(yy) {
			continue
		}
		m.hit(x, yy, w, "row:"+itoa(index))
		switch i {
		case 0, len(rows) + 1:
			l, rr := frame[0], frame[1]
			if i > 0 {
				l, rr = frame[2], frame[3]
			}
			cv.Set(x, yy, l, border.Bg(paper))
			for k := 1; k < w-1; k++ {
				cv.Set(x+k, yy, frame[4], border.Bg(paper))
			}
			cv.Set(x+w-1, yy, rr, border.Bg(paper))
			continue
		}
		row := rows[i-1]
		bg := paper
		if row.bg != 0 {
			bg = row.bg
			if selected {
				bg = Mix(row.bg, cSelBG, .35)
			}
		}
		cv.Set(x, yy, frame[5], border.Bg(paper))
		cv.Fill(x+1, yy, w-2, 1, Style{BG: bg})
		cv.Set(x+w-1, yy, frame[5], border.Bg(paper))
		if row.bar != 0 {
			cv.Set(x+1, yy, '▎', fg(row.bar).Bg(bg))
		}
		inner := w - 4
		rw := 0
		for _, s := range row.right {
			rw += width(s.s)
		}
		rw = min(rw, inner/2)
		lw := inner
		if rw > 0 {
			lw = inner - rw - 1
		}
		// Spans keep a background of their own, such as the state pill.
		on := func(st Style) Style {
			if st.BG == 0 {
				st.BG = bg
			}
			return st
		}
		cx := x + 2
		for _, s := range row.left {
			if cx >= x+2+lw {
				break
			}
			cx += cv.Text(cx, yy, ellipsis(s.s, x+2+lw-cx), on(s.st), x+2+lw-cx)
		}
		if rw > 0 {
			rx := x + 2 + inner - rw
			for _, s := range row.right {
				if rx >= x+2+inner {
					break
				}
				rx += cv.Text(rx, yy, ellipsis(s.s, x+2+inner-rx), on(s.st), x+2+inner-rx)
			}
		}
		if row.hit != "" {
			m.hit(x+1, yy, w-2, row.hit)
		}
	}
}
