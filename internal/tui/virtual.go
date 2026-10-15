package tui

import (
	"slices"
	"sort"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func (m *Model) drawVirtual(cv *Canvas, x, y, w, h int, nodes []mapNode, edges []mapEdge, groups map[string]Color, f mapFocus) {
	if !mapLayoutAllowed(w, h, len(nodes), len(edges)) {
		drawLayoutLimit(cv, x, y, w, h, len(nodes), len(edges))
		return
	}
	if len(nodes) == 0 {
		m.drawNetwork(cv, x, y, w, h, nodes, edges, groups, f)
		return
	}
	l := m.layoutVirtual(w, h, 0, nodes, edges, f)
	if l == nil {
		msg := "No node locations declared — add one with e (edit), or press m to change the map"
		cv.Text(x+max(0, (w-width(msg))/2), y+h/2, msg, fg(cMuted).italic(), w)
		return
	}
	// Routing is the costly part and between frames only the animation
	// moves, so the wires are kept while the layout holds. When some wires
	// find no way through, one column fewer leaves wider gaps between the
	// columns; the layout that lays more wires wins, within the same budget.
	key := l.key()
	if m.vcache.key != key {
		work := newLayoutWork()
		c := vCache{key: key, paths: l.routeBudget(work)}
		c.dropped = unrouted(c.paths)
		if c.dropped > 0 && l.cols > 1 && work.remaining > 0 {
			if l2 := m.layoutVirtual(w, h, l.cols-1, nodes, edges, f); l2 != nil {
				if p2 := l2.routeBudget(work); unrouted(p2) < c.dropped {
					c.cols, c.paths, c.dropped = l.cols-1, p2, unrouted(p2)
				}
			}
		}
		m.vcache = c
	}
	if m.vcache.cols > 0 {
		l = m.layoutVirtual(w, h, m.vcache.cols, nodes, edges, f)
	}
	// Each end of a wire meets its card's edge on one side of one row.
	type end struct {
		card, row int
		right     bool
	}
	ends := map[end][]int{} // the edges ending there; arrivals as -k-1
	ws := newWires(w, h)
	cellsOf := make([][][2]int, len(l.drawn))
	for k, d := range l.drawn {
		path := m.vcache.paths[k]
		if path == nil {
			continue
		}
		c, pri, dash := edgeStyle(d.e, f)
		cellsOf[k] = ws.route(corners(path, w), k, c, pri, dash)
		a, b := path[0], path[len(path)-1]
		ea := end{d.ca, l.rowOf[d.a], a%w > l.cards[d.ca].x}
		eb := end{d.cb, l.rowOf[d.b], b%w > l.cards[d.cb].x}
		ends[ea] = append(ends[ea], k)
		if d.e.Kind == edgeContain {
			ends[eb] = append(ends[eb], k)
		} else {
			ends[eb] = append(ends[eb], -k-1)
		}
	}
	ws.draw(cv, x, y)
	phase := m.phase()
	for k, cells := range cellsOf {
		if e := l.drawn[k].e; len(cells) >= 6 && (e.Signal == "up" || e.Signal == "carried") && e.Kind != edgeContain {
			ws.glint(cv, x, y, cells, k, e.ID, phase)
		}
	}
	var runs map[string][]span
	if m.FullMap {
		runs = m.nodeServices()
	}
	for _, c := range l.cards {
		m.drawVCard(cv, x, y, h, c, nodes, groups, f, runs)
	}
	// Where wires meet a card's frame: an arrowhead where one arrives, a tee
	// where they only leave, in the color of the one that needs the most
	// attention. A pill or chip has no frame, so its arrowhead stands just
	// outside it.
	keys := make([]end, 0, len(ends))
	for k := range ends {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.card != b.card {
			return a.card < b.card
		}
		if a.row != b.row {
			return a.row < b.row
		}
		return !a.right && b.right
	})
	for _, k := range keys {
		top, in := -1, false
		var c Color
		var best int8
		for _, e := range ends[k] {
			if e < 0 {
				in, e = true, -e-1
			}
			if ec, pri, _ := edgeStyle(l.drawn[e].e, f); top < 0 || pri > best {
				top, best, c = e, pri, ec
			}
		}
		c = Mix(c, cGlow, .2) // a touch brighter than the wire, so ends read
		card := l.cards[k.card]
		bx, by := card.x, card.y+k.row
		if k.right {
			bx = card.x + card.w - 1
		}
		if by < 0 || by >= h {
			continue
		}
		if card.h == 1 {
			if in {
				ax, r := bx-1, '▸'
				if k.right {
					ax, r = bx+1, '◂'
				}
				cv.Set(x+ax, y+by, r, fg(c))
			}
			continue
		}
		g := '┤'
		switch {
		case in && k.right:
			g = '◂'
		case in:
			g = '▸'
		case k.row == card.h-1:
			g = '┴' // the folded rows, at a corner of the frame
		case k.right:
			g = '├'
		}
		cv.Set(x+bx, y+by, g, fg(c).Bg(cv.at(x+bx, y+by).st.BG))
	}
	tag := " virtual geo "
	if tx := x + w - width(tag) - 1; tx > x {
		for i := tx; i < tx+width(tag); i++ {
			if cl := cv.at(i, y); cl.ch != " " || cl.st.FG != 0 {
				return
			}
		}
		cv.Text(tx, y, tag, fg(cFaint), -1)
	}
}

// drawVCard draws a place as a card: its name in the frame, one row per node
// with children indented under their parents and, where the card is wide
// enough, what runs on each node; along the foot, its position or how many
// rows are folded away. A node without a place is a pill.
func (m *Model) drawVCard(cv *Canvas, ox, oy, h int, c *vCard, nodes []mapNode, groups map[string]Color, f mapFocus, runs map[string][]span) {
	in := func(yy int) bool { return yy >= 0 && yy < h }
	paper := Mix(cInk, cKeyBG, .38)
	signal, focused := "", false
	for _, i := range c.rows {
		signal = worseSignal(signal, nodeSignal(nodes[i]))
		focused = focused || f.hasNode(nodes[i].ID)
	}
	if c.pill() {
		i := c.rows[0]
		n := nodes[i]
		if !in(c.y) {
			return
		}
		hue := providerColor(n.Provider, groups[n.Group])
		chip := Mix(hue, cInk, .84)
		st := fg(Mix(hue, cText, .35)).Bg(chip)
		if n.Kind == "external" {
			st = st.italic()
		}
		if focused {
			chip = cSelBG
			st = fg(cGlow).Bg(chip).bold()
		}
		sig := nodeSignal(n)
		cv.Set(ox+c.x, oy+c.y, ' ', Style{BG: chip})
		cv.Set(ox+c.x+1, oy+c.y, signalGlyph(sig), fg(signalColor(sig)).Bg(chip).bold())
		cv.Pad(ox+c.x+2, oy+c.y, " "+ellipsis(n.Name, c.w-4)+" ", st, c.w-2)
		if at := ox + c.x + 3 + width(n.Name); width(n.Name) <= c.w-4 {
			drawRuns(cv, at, oy+c.y, ox+c.x+c.w-1-at, runs[n.ID], chip, false)
		}
		m.hit(ox+c.x, oy+c.y, c.w, "select:node:"+n.ID)
		return
	}
	if c.chip() {
		if !in(c.y) {
			return
		}
		lead := c.rows[0]
		bg, st := paper, fg(cText).Bg(paper).bold()
		if focused {
			bg, st = cSelBG, fg(cGlow).Bg(cSelBG).bold()
			for _, i := range c.rows {
				if f.hasNode(nodes[i].ID) {
					lead = i
				}
			}
		}
		cv.Fill(ox+c.x, oy+c.y, c.w, 1, Style{BG: bg})
		glyphs := min(len(c.rows), 5)
		n := cv.Text(ox+c.x+1, oy+c.y, ellipsis(c.title(), c.w-3-glyphs), st, c.w-3-glyphs)
		for k, i := range c.rows[:glyphs] {
			sig := nodeSignal(nodes[i])
			cv.Set(ox+c.x+2+n+k, oy+c.y, signalGlyph(sig), fg(signalColor(sig)).Bg(bg))
		}
		m.hit(ox+c.x, oy+c.y, c.w, "select:node:"+nodes[lead].ID)
		return
	}
	frame := fg(Mix(cLine, cMuted, .3))
	switch {
	case focused:
		frame = fg(Mix(cLine, cAccent, .7))
	case signal == "down":
		frame = fg(Mix(cLine, cDown, .55))
	case signalRank(signal) >= 3:
		frame = fg(Mix(cLine, cWarn, .45))
	}
	corners := [6]rune{'╭', '╮', '╰', '╯', '─', '│'}
	switch {
	case !c.located:
		corners = [6]rune{'╭', '╮', '╰', '╯', '┄', '┆'}
	case focused && m.NoColor:
		corners = [6]rune{'┏', '┓', '┗', '┛', '━', '┃'}
	}
	frame = frame.Bg(paper)
	bottom := c.y + c.h - 1
	for yy := c.y; yy <= bottom; yy++ {
		if !in(yy) {
			continue
		}
		l, r, mid := corners[5], corners[5], ' '
		switch yy {
		case c.y:
			l, r, mid = corners[0], corners[1], corners[4]
		case bottom:
			l, r, mid = corners[2], corners[3], corners[4]
		}
		cv.Set(ox+c.x, oy+yy, l, frame)
		for xx := c.x + 1; xx < c.x+c.w-1; xx++ {
			cv.Set(ox+xx, oy+yy, mid, frame)
		}
		cv.Set(ox+c.x+c.w-1, oy+yy, r, frame)
	}
	lead := c.rows[0]
	if in(c.y) {
		ts := fg(cText).Bg(paper).bold()
		if !c.located {
			ts = fg(cMuted).Bg(paper).italic()
		}
		n := cv.Text(ox+c.x+2, oy+c.y, " "+ellipsis(c.title(), c.w-6)+" ", ts, c.w-4)
		m.hit(ox+c.x+2, oy+c.y, n, "select:node:"+nodes[lead].ID)
	}
	if in(bottom) {
		foot, fs := "", fg(cFaint).Bg(paper)
		if n := c.hidden(); n > 0 {
			foot, fs = "+"+itoa(n)+" more", fg(cMuted).Bg(paper)
		} else if c.located {
			foot = coords(c.lat, c.lon)
		}
		if foot != "" && width(foot)+4 <= c.w-2 {
			cv.Text(ox+c.x+c.w-width(foot)-4, oy+bottom, " "+foot+" ", fs, -1)
		}
	}
	lanes, nl := c.lanes(false)
	laneW := 0
	if nl > 0 {
		laneW = nl + 2
	}
	row := 0
	for k, i := range c.rows {
		if !c.shown[k] {
			continue
		}
		row++
		yy := c.y + row
		if !in(yy) {
			continue
		}
		n := nodes[i]
		bg := paper
		st := fg(Mix(groups[n.Group], cText, .35)).Bg(bg)
		if n.Kind == "external" {
			st = st.italic()
		}
		if f.hasNode(n.ID) {
			bg = cSelBG
			st = fg(cGlow).Bg(bg).bold()
		}
		inner := c.w - 2
		cv.Fill(ox+c.x+1, oy+yy, inner, 1, Style{BG: bg})
		if f.hasNode(n.ID) && m.NoColor {
			cv.Set(ox+c.x+1, oy+yy, '›', st)
		}
		cx := ox + c.x + 2
		if d := c.depth[k]; d > 0 {
			guide := '└'
			for j := k + 1; j < len(c.rows) && c.depth[j] >= d; j++ {
				if c.depth[j] == d && c.shown[j] {
					guide = '├'
					break
				}
			}
			cx += 2 * (d - 1)
			cv.Set(cx, oy+yy, guide, fg(cFaint).Bg(bg))
			cx += 2
		}
		sig := nodeSignal(n)
		cv.Set(cx, oy+yy, signalGlyph(sig), fg(signalColor(sig)).Bg(bg).bold())
		cx += 2
		lined := c.svc != nil && c.svc[k]
		if room := ox + c.x + c.w - 2 - laneW - cx; room > 0 {
			if nw := cv.Text(cx, oy+yy, ellipsis(n.Name, room), st, room); nw == width(n.Name) && !lined {
				drawRuns(cv, cx+nw, oy+yy, room-nw, runs[n.ID], bg, false)
			}
		}
		m.hit(ox+c.x, oy+yy, c.w, "select:node:"+n.ID)
		if lined {
			// What runs here, on a line of its own under the name.
			row++
			if yy++; in(yy) {
				cv.Fill(ox+c.x+1, oy+yy, inner, 1, Style{BG: paper})
				if room := ox + c.x + c.w - 2 - laneW - cx; room > 2 {
					rs := runs[n.ID]
					gc := runQuiet
					for _, r := range rs {
						if r.st.FG != runQuiet {
							gc = r.st.FG
							break
						}
					}
					if drawRuns(cv, cx, oy+yy, room, rs, paper, true) {
						cv.Set(cx, oy+yy, '◆', fg(gc).Bg(paper))
					}
				}
				m.hit(ox+c.x, oy+yy, c.w, "select:node:"+n.ID)
			}
		}
	}
	// The card's own links: each source row runs right into its lane, which
	// carries it to an arrowhead at the destination row.
	kx := c.x + c.w - 3 - nl
	for pass := 0; pass < 2; pass++ {
		for _, ln := range lanes {
			lx := c.x + c.w - 3 - ln.col
			if pass == 0 {
				for r := ln.lo; r <= ln.hi; r++ {
					var dirs uint8
					if r > ln.lo {
						dirs |= wN
					}
					if r < ln.hi {
						dirs |= wS
					}
					if r == ln.to || slices.Contains(ln.from, r) {
						dirs |= wW
					}
					if in(c.y + r) {
						cv.Set(ox+lx, oy+c.y+r, wireGlyph[dirs], fg(ln.c))
					}
				}
				continue
			}
			// Second pass: the runs into the lanes, over any lane they cross.
			for _, r := range append(append([]int{}, ln.from...), ln.to) {
				if !in(c.y + r) {
					continue
				}
				for xx := kx; xx < lx; xx++ {
					g := '─'
					if xx == kx && r == ln.to {
						g = '◂'
					}
					if xx == kx && r != ln.to && cv.at(ox+xx, oy+c.y+r).ch == "◂" {
						continue
					}
					cv.Set(ox+xx, oy+c.y+r, g, fg(ln.c))
				}
			}
		}
	}
}

// runQuiet is the voice of a service that has no problem.
var runQuiet = Mix(cMuted, cFaint, .35)

// nodeServices says what runs on each node: its services' names, those with
// a problem first and lit, the rest in a quiet voice.
func (m *Model) nodeServices() map[string][]span {
	s := m.Snapshot
	state := map[string]model.EntityState{}
	for _, e := range s.Services {
		state[e.ID] = e
	}
	type run struct {
		span
		rank int // 0 down, 1 degraded or affected, 2 fine
	}
	runs := map[string][]run{}
	for _, v := range s.Topology.Services {
		r := run{span{v.Name, fg(runQuiet)}, 2}
		if r.s == "" {
			r.s = v.ID
		}
		switch e := state[v.ID]; {
		case m.Cached() || m.Edit != nil:
		case e.Health == "down":
			r.st, r.rank = fg(Mix(cDown, cMuted, .2)), 0
		case e.Health == "degraded" || e.Affected:
			r.st, r.rank = fg(Mix(cWarn, cMuted, .2)), 1
		}
		runs[v.Node] = append(runs[v.Node], r)
	}
	out := map[string][]span{}
	for node, rs := range runs {
		// Trouble first, so it is what shows when not everything fits.
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].rank < rs[j].rank })
		for _, r := range rs {
			out[node] = append(out[node], r.span)
		}
	}
	return out
}

// runsWidth is what a row's services take after its name: a gap of two,
// then the names joined by ", ".
func runsWidth(runs []span) int {
	n := 0
	for _, r := range runs {
		n += 2 + width(r.s)
	}
	return n
}

// drawRuns writes what runs on a node in up to room cells from x, after a
// gap of two: as many whole names as fit, then how many more there are.
// When no whole name fits, a line of its own (alone) cuts the first short,
// or says how many there are; after a name, nothing is said. It reports
// whether it said anything.
func drawRuns(cv *Canvas, x, y, room int, runs []span, bg Color, alone bool) bool {
	x, room = x+2, room-2
	n, used := 0, 0
	for i, r := range runs {
		w := width(r.s)
		if i > 0 {
			w += 2
		}
		if used+w > room {
			break
		}
		n, used = n+1, used+w
	}
	for n > 0 && n < len(runs) && used+width(" +"+itoa(len(runs)-n)) > room {
		n--
		used -= width(runs[n].s)
		if n > 0 {
			used -= 2
		}
	}
	more := func(k int) string {
		if k < len(runs) {
			return " +" + itoa(len(runs)-k)
		}
		return ""
	}
	switch count := plural(len(runs), "service"); {
	case n == 0 && !alone:
		return false
	case n == 0 && room-width(more(1)) >= 8:
		x += cv.Text(x, y, ellipsis(runs[0].s, room-width(more(1))), runs[0].st.Bg(bg), -1)
		cv.Text(x, y, more(1), fg(cFaint).Bg(bg), -1)
		return true
	case n == 0 && width(count) <= room:
		cv.Text(x, y, count, fg(cFaint).Bg(bg), -1)
		return true
	case n == 0:
		return false
	}
	for i, r := range runs[:n] {
		if i > 0 {
			x += cv.Text(x, y, ", ", fg(cFaint).Bg(bg), -1)
		}
		x += cv.Text(x, y, r.s, r.st.Bg(bg), -1)
	}
	cv.Text(x, y, more(n), fg(cFaint).Bg(bg), -1)
	return true
}
