package tui

import "sort"

// The Overview answers "is anything wrong, and where?" before anything is
// read: a map of the network, a status card whose headline and big number
// carry the verdict, the list of what needs attention (selecting an item
// lights up its route on the map), a service matrix and the observers.

// bigGlyphs is a 3x5 pixel font drawn with half blocks, three rows tall.
var bigGlyphs = map[rune][5]string{
	'0': {"###", "#.#", "#.#", "#.#", "###"},
	'1': {".#.", "##.", ".#.", ".#.", "###"},
	'2': {"###", "..#", "###", "#..", "###"},
	'3': {"###", "..#", ".##", "..#", "###"},
	'4': {"#.#", "#.#", "###", "..#", "..#"},
	'5': {"###", "#..", "###", "..#", "###"},
	'6': {"###", "#..", "###", "#.#", "###"},
	'7': {"###", "..#", ".#.", ".#.", ".#."},
	'8': {"###", "#.#", "###", "#.#", "###"},
	'9': {"###", "#.#", "###", "..#", "###"},
	'/': {"..#", "..#", ".#.", "#..", "#.."},
	'-': {"...", "...", "###", "...", "..."},
}

// bigText draws s three cells tall and returns its width. Under NO_COLOR, or
// for characters the font lacks, it falls back to plain text.
func (m *Model) bigText(cv *Canvas, x, y int, s string, st Style) int {
	if m.NoColor {
		return cv.Text(x, y+1, s, st, -1)
	}
	cx := x
	for _, r := range s {
		g, ok := bigGlyphs[r]
		if !ok {
			cx += cv.Text(cx, y+2, string(r), st, -1)
			continue
		}
		for row := 0; row < 3; row++ {
			for col := 0; col < 3; col++ {
				top := g[row*2][col] == '#'
				bottom := row*2+1 < 5 && g[row*2+1][col] == '#'
				ch := ' '
				switch {
				case top && bottom:
					ch = '█'
				case top:
					ch = '▀'
				case bottom:
					ch = '▄'
				}
				if ch != ' ' {
					cv.Set(cx+col, y+row, ch, st)
				}
			}
		}
		cx += 4
	}
	return cx - x - 1
}

// verdict is the one-line answer at the top of the Overview.
func (m *Model) verdict() (string, Color) {
	up, down, warn, unk := m.counts()
	inc := len(m.Snapshot.Incidents)
	switch {
	case m.Edit != nil:
		return "◇ LOCAL DRAFT", cEdit
	case m.Cached():
		return "◌ CACHED", cWarn
	case up+down+warn+unk == 0:
		return "○ NO CHECKS", cUnknown
	case inc > 0:
		return "✖ " + itoa(inc) + map[bool]string{true: " INCIDENT", false: " INCIDENTS"}[inc == 1], cDown
	case down > 0:
		return "✖ " + itoa(down) + " DOWN", cDown
	case warn > 0:
		return "▲ DEGRADED", cWarn
	case up == 0:
		return "○ NO EVIDENCE", cUnknown
	}
	return "● ALL CLEAR", cUp
}

func (m *Model) statusCard(cv *Canvas, x, y, w, h int) {
	up, down, warn, unk := m.counts()
	total := up + down + warn + unk
	word, c := m.verdict()
	cv.Text(x, y, word, fg(c).bold(), w)
	when := "updated " + age(m.Now, m.Snapshot.GeneratedAt)
	if m.Edit != nil {
		when = "no live evidence"
	}
	if width(word)+width(when)+2 <= w {
		cv.Text(x+w-width(when), y, when, fg(cFaint), -1)
	}
	row := y + 1
	if h >= 8 {
		// The big number is the failures when there are any, otherwise
		// what passes.
		n, label, nc := up, "checks passing", cUp
		switch {
		case m.Cached() || m.Edit != nil || total == 0:
			n, label, nc = unk, "checks without current evidence", cUnknown
		case down > 0:
			n, label, nc = down, "checks failing", cDown
		case warn > 0:
			n, label, nc = warn, "checks warning", cWarn
		}
		bw := m.bigText(cv, x, row+1, itoa(n), fg(nc).bold())
		lx := x + bw + 3
		cv.Text(lx, row+1, label, fg(cText), x+w-lx)
		cv.Text(lx, row+2, "of "+itoa(total)+" configured", fg(cMuted), x+w-lx)
		row += 5
	}
	if row < y+h {
		if m.NoColor {
			row--
		} else {
			m.healthBar(cv, x, row, w, up, warn, down, unk, '━')
		}
		row++
	}
	if row < y+h {
		cx := x
		count := func(g rune, gc Color, n int, word string) {
			st := fg(cFaint)
			if n > 0 {
				st = fg(gc)
			}
			cv.Set(cx, row, g, st)
			cx += 2
			ns := fg(cFaint)
			if n > 0 {
				ns = fg(cText).bold()
			}
			cx += cv.Text(cx, row, itoa(n), ns, -1)
			cx += cv.Text(cx, row, " "+word+"   ", fg(cMuted), max(0, x+w-cx))
		}
		count('●', cUp, up, "up")
		count('▲', cWarn, warn, "warn")
		count('✖', cDown, down, "down")
		count('○', cUnknown, unk, "unknown")
		row++
	}
	if row < y+h {
		inc := len(m.Snapshot.Incidents)
		cx := x
		ic := cFaint
		if inc > 0 {
			ic = cDown
		}
		cv.Set(cx, row, '⚑', fg(ic))
		cx += 2
		cx += cv.Text(cx, row, itoa(inc)+" incidents", fg(cMuted), -1) + 4
		live, all := m.liveObservers(), len(m.Snapshot.Observers)
		oc := cAccent2
		if live < all {
			oc = cWarn
		}
		cv.Set(cx, row, '◎', fg(oc))
		cx += 2
		cv.Text(cx, row, itoa(live)+"/"+itoa(all)+" observers live", fg(cMuted), max(0, x+w-cx))
	}
}

// servicesCard is a matrix of every service, problems first.
func (m *Model) servicesCard(cv *Canvas, x, y, w, h int) {
	rows := m.tabRows(tabServices)
	sort.SliceStable(rows, func(i, j int) bool { return serviceRank(rows[i]) < serviceRank(rows[j]) })
	if len(rows) == 0 {
		cv.Text(x, y, "No services declared.", fg(cMuted).italic(), w)
		return
	}
	cellW := 0
	for _, r := range rows {
		cellW = max(cellW, width(r.Title)+4)
	}
	cols := max(1, min(w/min(cellW, 26), (len(rows)+h-1)/max(1, h)+1))
	cols = max(1, min(cols, w/12))
	cellW = w / cols
	for i, r := range rows {
		cx, cy := x+(i/h)*cellW, y+i%h
		if i/h >= cols {
			more := "+" + itoa(len(rows)-i) + " more"
			cv.Text(x+w-width(more), y+h-1, more, fg(cFaint), -1)
			break
		}
		if i%h == h-1 && i/h == cols-1 && i < len(rows)-1 {
			more := "+" + itoa(len(rows)-i) + " more"
			cv.Text(cx, cy, more, fg(cFaint), -1)
			break
		}
		h0, note := r.Health, r.Note
		if m.Cached() {
			h0, note = "unknown", ""
		}
		g, gc := healthGlyph(h0), healthColor(h0)
		if note == "affected" {
			g, gc = '▲', cWarn
		}
		ts := fg(cMuted)
		if h0 == "down" || h0 == "degraded" || note == "affected" {
			ts = fg(cText)
		}
		cv.Set(cx, cy, g, fg(gc))
		n := cv.Text(cx+2, cy, ellipsis(r.Title, cellW-3), ts, cellW-3)
		m.hit(cx, cy, n+2, "goto:service:"+r.ID)
	}
}

func serviceRank(r Row) int {
	switch {
	case r.Health == "down":
		return 0
	case r.Health == "degraded":
		return 1
	case r.Note == "affected":
		return 2
	case r.Health == "unknown":
		return 4
	}
	return 3
}

// observersCard lists each vantage point with the age of its last evidence.
func (m *Model) observersCard(cv *Canvas, x, y, w, h int) {
	rows := m.tabRows(tabObservers)
	if len(rows) == 0 {
		cv.Text(x, y, "No observers declared.", fg(cMuted).italic(), w)
		return
	}
	for i, r := range rows {
		if i >= h {
			break
		}
		if i == h-1 && len(rows) > h {
			cv.Text(x, y+i, "+"+itoa(len(rows)-i)+" more", fg(cFaint), -1)
			break
		}
		hl := r.Health
		if m.Cached() {
			hl = "unknown"
		}
		cv.Set(x, y+i, healthGlyph(hl), fg(healthColor(hl)))
		last := ""
		if len(r.Cols) > 1 {
			last = r.Cols[1]
		}
		lw := width(last)
		n := cv.Text(x+2, y+i, r.Title, fg(cText), w-lw-4)
		cv.Text(x+w-lw, y+i, last, fg(cFaint), -1)
		m.hit(x, y+i, n+2, "goto:observer:"+r.ID)
	}
}

// overview lays the dashboard out for the space it has: a map beside a
// sidebar when wide, the map over the cards when medium, cards alone when
// small.
func (m *Model) overview(cv *Canvas, x, y, w, h int, rows []Row, sel Row, tag string) {
	t := m.Snapshot.Topology
	mapTitle := "NETWORK"
	mapTag := "· " + itoa(len(t.Nodes)) + " nodes · " + itoa(len(t.Links)) + " links · m z"
	attention := func(x, y, w, h int, active bool) {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, "NEEDS ATTENTION", tag, active)
		if len(rows) > 0 || m.Search != "" {
			m.drawList(cv, ix, iy, iw, ih, rows)
			return
		}
		msg, sub, c := "✓ Nothing needs attention", "Every check in this view passes; no incidents.", cUp
		switch {
		case m.Edit != nil:
			msg, sub, c = "◇ Local draft", "The editor has no live evidence; preview it with topo preview --config.", cEdit
		case m.Cached():
			msg, sub, c = "◌ Status not established", "Showing the last snapshot; nothing current is known.", cWarn
		case len(m.Snapshot.Checks) == 0:
			msg, sub, c = "○ No checks in this view", "Nothing is measured from here yet.", cUnknown
		}
		cv.Text(ix, iy, msg, fg(c).bold(), iw)
		for i, l := range wrap(sub, iw) {
			if 1+i < ih {
				cv.Text(ix, iy+1+i, l, fg(cMuted), iw)
			}
		}
	}
	nServices := len(t.Services)
	nObservers := len(m.Snapshot.Observers)
	switch {
	case w >= 140 && h >= 24:
		sw := min(max(w*36/100, 46), 74)
		mw := w - sw
		mh := h
		if m.mapMode() == "geo" {
			if need := m.geoRows(mw-2) + 2 + m.mapChrome(); h-need >= 9 {
				mh = need
			}
		}
		ix, iy, iw, ih := m.panel(cv, x, y, mw, mh, mapTitle, mapTag, false)
		m.drawMap(cv, ix-1, iy, iw+2, ih, sel)
		sh := 11
		if h < 34 {
			sh = 6
		}
		ix, iy, iw, ih = m.panel(cv, x+mw, y, sw, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		left := h - sh
		oh := min(nObservers, 6) + 2
		if mh < h {
			// The map letterboxes: attention goes under it, the sidebar
			// keeps services and observers.
			attention(x, y+mh, mw, h-mh, true)
			vh := left - oh
			ix, iy, iw, ih = m.panel(cv, x+mw, y+sh, sw, vh, "SERVICES", "· "+itoa(nServices), false)
			m.servicesCard(cv, ix, iy, iw, ih)
			ix, iy, iw, ih = m.panel(cv, x+mw, y+sh+vh, sw, oh, "OBSERVERS", "", false)
			m.observersCard(cv, ix, iy, iw, ih)
			return
		}
		vh := min((nServices+1)/2+2, max(4, left/3))
		if len(rows) == 0 && m.Search == "" {
			// Nothing to attend to: a short all-clear, and the room goes
			// to the service matrix.
			vh = max(vh, left-oh-5)
		}
		if left-oh-vh < 5 {
			oh = 0
		}
		ah := left - vh - oh
		attention(x+mw, y+sh, sw, ah, true)
		ix, iy, iw, ih = m.panel(cv, x+mw, y+sh+ah, sw, vh, "SERVICES", "· "+itoa(nServices), false)
		m.servicesCard(cv, ix, iy, iw, ih)
		if oh > 0 {
			ix, iy, iw, ih = m.panel(cv, x+mw, y+sh+ah+vh, sw, oh, "OBSERVERS", "", false)
			m.observersCard(cv, ix, iy, iw, ih)
		}
	case w >= 90 && h >= 26:
		mh := h * 52 / 100
		if m.mapMode() == "geo" {
			mh = min(mh, m.geoRows(w-2)+2+m.mapChrome())
		}
		ix, iy, iw, ih := m.panel(cv, x, y, w, mh, mapTitle, mapTag, false)
		m.drawMap(cv, ix-1, iy, iw+2, ih, sel)
		sw := min(max(w*42/100, 44), 64)
		ix, iy, iw, ih = m.panel(cv, x, y+mh, sw, h-mh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		if ah := 5; len(rows) == 0 && m.Search == "" && h-mh-ah >= 5 {
			attention(x+sw, y+mh, w-sw, ah, true)
			ix, iy, iw, ih = m.panel(cv, x+sw, y+mh+ah, w-sw, h-mh-ah, "SERVICES", "· "+itoa(nServices), false)
			m.servicesCard(cv, ix, iy, iw, ih)
			return
		}
		attention(x+sw, y+mh, w-sw, h-mh, true)
	default:
		sh := min(6, h/2)
		ix, iy, iw, ih := m.panel(cv, x, y, w, sh, "STATUS", "", false)
		m.statusCard(cv, ix, iy, iw, ih)
		attention(x, y+sh, w, h-sh, true)
	}
}
