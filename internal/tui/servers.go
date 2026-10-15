package tui

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
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

// cardRow is one line inside a card: spans from the left, spans pushed to
// the right edge, an optional tint and a click target.
type cardRow struct {
	left, right []span
	bg          Color
	bar         Color // a stripe at the card's inner left edge
	rule        bool  // a hairline runs on from the left spans to the edge
	hit         string
}

// fleet is what the cards are built from, indexed once per frame, with the
// hue of each node group.
type fleet struct {
	*view.Fleet
	groups map[string]Color
}

func (m *Model) fleet() fleet { return fleet{m.view().Fleet(), m.groupColors()} }

// netColors tint the network of an address so a card shows at a glance
// what is public and what only travels the tailnet or the LAN.
var netColors = map[string]Color{"public": cAccent2, "tailnet": cEdit, "lan": Hex(0x86EFAC), "loopback": cMuted, "external": cFaint}

// card builds the rows of a server's card for content width w.
func (m *Model) card(f fleet, r Row, w int, selected bool) []cardRow {
	n := f.Nodes[r.ID]
	c := f.Card(r.ID)
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
	// A section opens with a quiet label and a hairline, so the name and
	// what needs attention stay the loudest things on the card.
	heading := func(title string, count int) {
		add(cardRow{})
		head := []span{{title, fg(Mix(cMuted, cAccent, .3)).bold()}}
		if count > 1 {
			head = append(head, span{" " + itoa(count), fg(cFaint)})
		}
		add(cardRow{left: head, rule: true})
	}

	// The header: what it is and its state, then its kind and where it is.
	// A server with a problem takes the problem's color, so it stands out
	// on the wall.
	pill, pillStyle := statePill(r)
	name := fg(cText).bold()
	if selected {
		name = fg(cGlow).bold()
	}
	switch {
	case r.Health == "down":
		band = Mix(cDown, cInk, .84)
	case r.Health == "degraded" || r.Note == "affected":
		band = Mix(cWarn, cInk, .86)
	}
	add(cardRow{left: []span{{r.Title, name}}, right: []span{{pill, pillStyle}}, bg: band, bar: hue})
	sub := []span{{c.Kind, fg(Mix(hue, cText, .35))}}
	if c.Group != "" {
		sub = append(sub, span{" · " + c.Group, fg(cMuted)})
	}
	where := []span{}
	switch {
	case n.Location != nil:
		where = []span{{"◎ " + c.Where, fg(cMuted)}}
	case n.Kind == "edge":
		where = []span{{"◌ " + c.Where, fg(cMuted)}}
	case c.Where != "":
		where = []span{{c.Where, fg(cMuted)}}
	}
	add(cardRow{left: sub, right: where, bg: band, bar: hue})
	if len(c.Resources) > 0 {
		heading("LOCAL RESOURCES", len(c.Resources))
		for _, resource := range c.Resources {
			hit := "goto:check:" + resource.Check
			add(cardRow{left: []span{{string(healthGlyph(resource.Health)) + " ", fg(healthColor(resource.Health))}, {resource.Observer.Name, fg(cMuted)}}, right: []span{{resource.Freshness, fg(cMuted)}}, hit: hit})
			for _, pair := range view.ResourceValues(resource) {
				color := cText
				if resource.Health == "degraded" {
					color = cWarn
				}
				if resource.Health == "unknown" {
					color = cFaint
				}
				add(cardRow{left: []span{{pair[0], fg(cMuted)}}, right: []span{{pair[1], fg(color)}}, hit: hit})
			}
		}
	}

	// What runs here, healthiest last so trouble reads first.
	if len(c.Services) > 0 {
		title := "SERVICES"
		if hosted(n.Kind) {
			title = "RUNS HERE"
		}
		heading(title, len(c.Services))
		for i, v := range c.Services {
			if i == most-1 && more(len(c.Services)) {
				break
			}
			g, gc := healthGlyph(v.Health), healthColor(v.Health)
			if v.Affected && v.Health != "down" {
				g, gc = '▲', cWarn
			}
			right := []span{}
			switch {
			case len(v.Via) > 0:
				names := []string{}
				for _, ref := range v.Via {
					names = append(names, ref.Name)
				}
				right = append(right, span{"via " + strings.Join(names, " ⇢ "), fg(providerColor(f.Nodes[v.Via[0].ID].Provider, cMuted))})
			case v.Runtime != "":
				right = append(right, span{v.Runtime, fg(Mix(hue, cText, .2))})
			case !v.Monitored:
				right = append(right, span{"not monitored", fg(cFaint)})
			}
			add(cardRow{left: []span{{string(g) + " ", fg(gc)}, {v.Name, fg(cText)}}, right: right, hit: "goto:service:" + v.ID})
		}
	}

	// An edge: the addresses it answers for, and where it answered from.
	if len(c.Fronts) > 0 {
		heading("IN FRONT OF", len(c.Fronts))
		for i, ep := range c.Fronts {
			if i == most-1 && more(len(c.Fronts)) {
				break
			}
			add(cardRow{left: []span{{string(healthGlyph(ep.Health)) + " ", fg(healthColor(ep.Health))}, {hostOf(ep.Address), fg(cText)}}, right: []span{{"→ " + ep.Node.Name, fg(cMuted)}}, hit: "select:endpoint:" + ep.ID})
		}
	}
	if len(c.Pops) > 0 {
		heading("ANSWERED FROM", 0)
		add(cardRow{left: []span{{strings.Join(c.Pops, " · "), fg(Mix(hue, cText, .25))}}, right: []span{{"response headers", fg(cFaint)}}})
	}

	// How it is reached.
	if len(c.Addresses) > 0 {
		heading("ADDRESSES", len(c.Addresses))
		for i, ep := range c.Addresses {
			if i == most-1 && more(len(c.Addresses)) {
				break
			}
			right := []span{}
			if len(ep.Via) > 0 {
				right = append(right, span{"⇠ " + ep.Via[0].Name + " ", fg(providerColor(f.Nodes[ep.Via[0].ID].Provider, cMuted))})
			}
			if ep.Health == "" {
				right = append(right, span{"·", fg(cFaint)})
			} else {
				right = append(right, span{string(healthGlyph(ep.Health)), fg(healthColor(ep.Health))})
			}
			nc, ok := netColors[ep.Network]
			if !ok {
				nc = cMuted
			}
			add(cardRow{left: []span{{fit(ep.Network, 9), fg(nc)}, {ep.Address, fg(cText)}}, right: right, hit: "select:endpoint:" + ep.ID})
		}
	}

	// What it hosts.
	if len(c.Hosts) > 0 {
		heading("HOSTS", len(c.Hosts))
		for i, k := range c.Hosts {
			if i == most-1 && more(len(c.Hosts)) {
				break
			}
			tree := "├ "
			if i == len(c.Hosts)-1 {
				tree = "└ "
			}
			add(cardRow{left: []span{{tree, fg(cFaint)}, {string(healthGlyph(k.Health)) + " ", fg(healthColor(k.Health))}, {k.Name, fg(cText)}}, right: []span{{strings.Join(k.Runs, ", "), fg(cMuted)}}, hit: "select:node:" + k.ID})
		}
	}

	// Who observes from here.
	if len(c.Observers) > 0 {
		heading("OBSERVES FROM HERE", len(c.Observers))
		for _, o := range c.Observers {
			oc := healthColor(o.Health)
			if o.Outdated {
				oc = cWarn
			}
			add(cardRow{left: []span{{"◎ ", fg(oc)}, {o.Name, fg(cText)}}, right: []span{{age(m.Now, o.LastSeen), fg(cFaint)}}, hit: "goto:observer:" + o.ID})
		}
	}

	// Its declared connections.
	if len(c.Links) > 0 {
		heading("LINKS", len(c.Links))
		for i, l := range c.Links {
			if i == 4 && len(c.Links) > 5 {
				add(cardRow{left: []span{{"+" + itoa(len(c.Links)-4) + " more", fg(cFaint)}}})
				break
			}
			arrow := "→ "
			if l.Inbound {
				arrow = "← "
			}
			add(cardRow{left: []span{{arrow, fg(cAccent)}, {l.Other.Name, fg(cText)}}, right: []span{{l.Transport, fg(cFaint)}}, hit: "select:link:" + l.ID})
		}
	}

	// The operator's notes, as a memo.
	if c.Memo != "" {
		add(cardRow{})
		paper := Mix(cInk, cCity, .07)
		for i, line := range memoLines(c.Memo, w-2) {
			lead := "  "
			if i == 0 {
				lead = "✎ "
			}
			add(cardRow{left: []span{{lead, fg(Mix(cCity, cInk, .2))}, {line, fg(Mix(cText, cCity, .18)).italic()}}, bg: paper, bar: Mix(cCity, cInk, .35)})
		}
	}
	return rows
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
		// The frame stands on the ground and only its inside is paper, so
		// the line is the card's edge.
		switch i {
		case 0, len(rows) + 1:
			l, rr := frame[0], frame[1]
			if i > 0 {
				l, rr = frame[2], frame[3]
			}
			cv.Set(x, yy, l, border)
			for k := 1; k < w-1; k++ {
				cv.Set(x+k, yy, frame[4], border)
			}
			cv.Set(x+w-1, yy, rr, border)
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
		cv.Set(x, yy, frame[5], border)
		cv.Fill(x+1, yy, w-2, 1, Style{BG: bg})
		cv.Set(x+w-1, yy, frame[5], border)
		if row.bar != 0 {
			cv.Set(x+1, yy, '▎', fg(row.bar).Bg(bg))
		}
		// What a row names keeps its width first; what it says on the right
		// stays whole when short (a state, an age) and otherwise gets what
		// is left, but never less than a third.
		inner := w - 4
		lw0, rw := 0, 0
		for _, s := range row.left {
			lw0 += width(s.s)
		}
		for _, s := range row.right {
			rw += width(s.s)
		}
		if rw > 12 && lw0+1+rw > inner {
			rw = max(min(rw, inner-lw0-1), min(rw, 12, inner/3))
		}
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
		if row.rule {
			for k := cx + 1; k < x+2+inner; k++ {
				cv.Set(k, yy, '─', fg(cLine).Bg(bg))
			}
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
