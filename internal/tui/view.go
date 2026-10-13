package tui

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func (m *Model) profile() int {
	if m.NoColor {
		return colorNone
	}
	return m.Colors
}

// Frame is a full repaint; the run loop uses Render and repaints changed lines.
func (m *Model) Frame() string {
	lines := m.Render()
	if m.Width < 30 || m.Height < 10 {
		return "\x1b[H" + lines[0] + "\x1b[J"
	}
	return "\x1b[H" + strings.Join(lines, "\r\n")
}

func (m *Model) Render() []string {
	w, h := m.Width, m.Height
	m.hits = m.hits[:0]
	if w < 30 || h < 10 {
		return []string{fit("Topophany: resize terminal (30x10 minimum)", max(w, 1))}
	}
	cv := NewCanvas(w, h)
	m.header(cv)
	m.tabBar(cv, 1)
	m.statusBar(cv, 2)
	m.body(cv, 0, 3, w, h-4)
	m.footer(cv, h-1)
	if m.Debug {
		m.drawDebug(cv)
	}
	if m.modal != nil {
		cv.Each(func(s Style) Style {
			c := s.FG
			if c == 0 {
				c = cText
			}
			return Style{FG: Mix(c, cDeep, .62)}
		})
		m.drawingModal = true
		m.drawModal(cv)
		m.drawingModal = false
	}
	return cv.Lines(m.profile())
}

func (m *Model) header(cv *Canvas) {
	w := cv.W
	bar := Style{BG: cPanelBG}
	cv.Fill(0, 0, w, 1, bar)
	x := 1
	cv.Set(x, 0, '◉', fg(cAccent).Bg(cPanelBG).bold())
	x += 2
	for i, r := range "TOPOPHANY" {
		cv.Set(x+i, 0, r, fg(Mix(cAccent, cAccent2, float64(i)/8)).Bg(cPanelBG).bold())
	}
	x += 9
	view := m.Observer
	if view == "" {
		view = "all observers"
	}
	x += 2
	x += cv.Text(x, 0, m.Snapshot.Project, fg(cText).Bg(cPanelBG).bold(), w-x-1)
	x += cv.Text(x, 0, "  ·  view: ", fg(cMuted).Bg(cPanelBG), w-x-1)
	x += cv.Text(x, 0, view, fg(cAccent2).Bg(cPanelBG), w-x-1)
	var right []struct {
		s  string
		st Style
	}
	add := func(s string, c Color) {
		right = append(right, struct {
			s  string
			st Style
		}{s, fg(c).Bg(cPanelBG)})
	}
	if m.Edit != nil {
		add("✎ LOCAL DRAFT ", cEdit)
		add(m.Edit.Path, cMuted)
		if m.Edit.Dirty {
			add("  ● unsaved", cWarn)
		} else {
			add("  ✓ saved", cUp)
		}
	} else {
		if m.Preview != nil {
			add("◆ PREVIEW ", cEdit)
			add(m.scenario(), cText)
		} else {
			if m.Snapshot.Instance != "" {
				add(m.Snapshot.Instance, cText)
				add(" @ ", cFaint)
			}
			add(m.Server, cMuted)
		}
		add("  ·  "+age(m.Now, m.Snapshot.GeneratedAt)+"  ", cMuted)
		if m.Cached() {
			add("◌ CACHED", cWarn)
		} else {
			// The live dot breathes with each repaint, never faster than data.
			add("● LIVE", Mix(cUp, cGlow, .5+.5*float64(m.Now.Second()%2)))
		}
	}
	total := 0
	for _, r := range right {
		total += width(secure.Text(r.s))
	}
	rx := w - 1 - total
	if rx < x+2 {
		// Not enough room: keep the trailing state indicator only.
		last := right[len(right)-1]
		cv.Text(w-1-width(last.s), 0, last.s, last.st, -1)
		return
	}
	for _, r := range right {
		rx += cv.Text(rx, 0, r.s, r.st, -1)
	}
}

func (m *Model) tabBar(cv *Canvas, y int) {
	labels := make([]string, len(tabs))
	total := 0
	for i, t := range tabs {
		labels[i] = itoa(i+1) + " " + t
		total += width(labels[i]) + 3
	}
	short := total > cv.W
	x := 1
	for i := range tabs {
		label := labels[i]
		if short && i != m.Tab {
			label = itoa(i + 1)
		}
		text := " " + label + " "
		st := fg(cMuted)
		if i == m.Tab {
			st = fg(cDeep).Bg(cAccent).bold()
			if m.NoColor {
				text = "[" + label + "]"
			}
		}
		n := cv.Text(x, y, text, st, -1)
		m.hit(x, y, n, "tab:"+itoa(i))
		x += n + 1
		if x >= cv.W {
			break
		}
	}
}

func (m *Model) counts() (up, down, warn, unk int) {
	for _, s := range m.Snapshot.Checks {
		if m.Cached() {
			unk++
			continue
		}
		switch s.Health {
		case "up":
			up++
		case "down":
			down++
		case "degraded":
			warn++
		default:
			unk++
		}
	}
	return
}

func (m *Model) statusBar(cv *Canvas, y int) {
	x := 1
	if m.notice != "" && m.Now.Sub(m.noticeAt) < 8e9 {
		c, g := cUp, "✓ "
		if m.noticeErr {
			c, g = cDown, "✖ "
		}
		cv.Text(x, y, g+m.notice, fg(c), cv.W-2)
		return
	}
	if m.Error != "" {
		cv.Text(x, y, "⚠ OFFLINE / CACHED: "+m.Error, fg(cWarn).bold(), cv.W-2)
		return
	}
	up, down, warn, unk := m.counts()
	chip := func(label string, c Color, n int, word string) {
		if x >= cv.W-4 {
			return
		}
		x += cv.Text(x, y, label, fg(c).bold(), -1)
		x += cv.Text(x, y, " "+itoa(n)+" "+word+"   ", fg(cMuted), -1)
	}
	x += cv.Text(x, y, "CHECKS  ", fg(cFaint).bold(), -1)
	chip("●", cUp, up, "up")
	chip("✖", cDown, down, "down")
	chip("▲", cWarn, warn, "warning")
	chip("○", cUnknown, unk, "unknown")
	x += cv.Text(x, y, "│  ", fg(cLine), -1)
	chip("⚑", cDown, len(m.Snapshot.Incidents), "incidents")
	live := 0
	for _, o := range m.Snapshot.Observers {
		if o.Health == "up" && !m.Cached() {
			live++
		}
	}
	if x < cv.W-20 {
		x += cv.Text(x, y, "│  ", fg(cLine), -1)
		x += cv.Text(x, y, "◎ ", fg(cAccent2).bold(), -1)
		cv.Text(x, y, itoa(live)+"/"+itoa(len(m.Snapshot.Observers))+" observers live", fg(cMuted), cv.W-x-1)
	}
}

// panel draws a rounded frame with a title and returns the inner rectangle.
func (m *Model) panel(cv *Canvas, x, y, w, h int, title, tag string, active bool) (int, int, int, int) {
	border := fg(cLine)
	if active {
		border = fg(Mix(cLine, cAccent, .45))
	}
	cv.Box(x, y, w, h, border)
	if title != "" && w > 6 {
		n := cv.Text(x+2, y, " "+title+" ", fg(cAccent).bold(), w-4)
		if tag != "" && x+2+n+width(tag)+3 < x+w {
			cv.Text(x+2+n, y, tag+" ", fg(cFaint), w-4-n)
		}
	}
	return x + 2, y + 1, max(0, w-4), max(0, h-2)
}

func (m *Model) body(cv *Canvas, x, y, w, h int) {
	rows := m.Rows()
	if m.Selected >= len(rows) {
		m.Selected = max(0, len(rows)-1)
	}
	sel := Row{}
	if len(rows) > 0 {
		sel = rows[m.Selected]
	}
	title := strings.ToUpper(tabs[m.Tab])
	tag := "· " + itoa(len(rows))
	if m.Search != "" {
		tag += " matching “" + m.Search + "”"
	}
	if m.Tab == tabAbout {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, "ABOUT", "· the appearance of place", false)
		m.drawAbout(cv, ix-1, iy, iw+2, ih)
		return
	}
	if m.Detail {
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, title+" · "+sel.Title, "Esc/Enter back · ↑↓ scroll", true)
		m.drawDetail(cv, ix, iy, iw, ih, sel, true)
		return
	}
	switch {
	case m.Tab == tabTopology:
		mode := "geo map"
		if m.mapMode() == "network" {
			mode = "network graph"
		}
		if w >= 100 {
			lw := min(max(w*36/100, 34), 60)
			mw := w - lw
			ix, iy, iw, ih := m.panel(cv, x, y, mw, h, title+" · "+mode, "m switch · z zoom", false)
			m.drawMap(cv, ix-1, iy, iw+2, ih, sel)
			lh := h
			if h >= 16 {
				lh = h * 55 / 100
			}
			ix, iy, iw, ih = m.panel(cv, x+mw, y, lw, lh, "NODES · LINKS", tag, true)
			m.drawList(cv, ix, iy, iw, ih, rows)
			if lh < h {
				ix, iy, iw, ih = m.panel(cv, x+mw, y+lh, lw, h-lh, "EVIDENCE", "", false)
				m.drawDetail(cv, ix, iy, iw, ih, sel, false)
			}
			return
		}
		if h >= 16 {
			mh := h * 55 / 100
			ix, iy, iw, ih := m.panel(cv, x, y, w, mh, title+" · "+mode, "m · z", false)
			m.drawMap(cv, ix-1, iy, iw+2, ih, sel)
			ix, iy, iw, ih = m.panel(cv, x, y+mh, w, h-mh, "NODES · LINKS", tag, true)
			m.drawList(cv, ix, iy, iw, ih, rows)
			return
		}
	case m.Tab == tabOverview && h >= 14 && w >= 60:
		m.cards(cv, x, y, w)
		y, h = y+4, h-4
	}
	if w >= 100 {
		lw := w / 2
		ix, iy, iw, ih := m.panel(cv, x, y, lw, h, title, tag, true)
		m.drawList(cv, ix, iy, iw, ih, rows)
		ix, iy, iw, ih = m.panel(cv, x+lw, y, w-lw, h, "EVIDENCE", "Enter expand", false)
		m.drawDetail(cv, ix, iy, iw, ih, sel, false)
		return
	}
	ix, iy, iw, ih := m.panel(cv, x, y, w, h, title, tag, true)
	m.drawList(cv, ix, iy, iw, ih, rows)
}

func (m *Model) drawList(cv *Canvas, x, y, w, h int, rows []Row) {
	if h <= 0 {
		return
	}
	if m.Selected < m.Offset {
		m.Offset = m.Selected
	}
	if m.Selected >= m.Offset+h {
		m.Offset = m.Selected - h + 1
	}
	if len(rows) == 0 {
		msg := "No matching entries / no current evidence."
		if m.Edit != nil && m.Search == "" {
			msg = "Nothing here yet — press a to add."
		}
		cv.Text(x, y, msg, fg(cMuted).italic(), w)
		return
	}
	for i := 0; i < h; i++ {
		j := m.Offset + i
		if j >= len(rows) {
			break
		}
		r := rows[j]
		selected := j == m.Selected
		base := Style{}
		if selected {
			base = Style{BG: cSelBG}
			cv.Fill(x-1, y+i, w+2, 1, base)
			cv.Set(x-1, y+i, '▌', fg(cAccent).Bg(cSelBG))
		}
		cx := x
		if m.NoColor {
			mark := "  "
			if selected {
				mark = "› "
			}
			cx += cv.Text(cx, y+i, mark, base, -1)
		}
		cx += cv.Text(cx, y+i, badge(r.Health), base.Fg(healthColor(r.Health)).bold(), x+w-cx)
		cx += cv.Text(cx, y+i, "  ", base, x+w-cx)
		ts := base.Fg(cText)
		if selected {
			ts = ts.Fg(cGlow).bold()
		}
		if r.Kind == "endpoint" || r.Kind == "link" {
			ts = ts.Fg(Mix(cText, cMuted, .4))
			if selected {
				ts = ts.Fg(cGlow)
			}
		}
		cx += cv.Text(cx, y+i, r.Title, ts, x+w-cx)
		if r.Meta != "" && cx < x+w-3 {
			cx += cv.Text(cx, y+i, "  · "+r.Meta, base.Fg(cMuted), x+w-cx)
		}
		m.hit(x-1, y+i, w+2, "row:"+itoa(j))
	}
	if len(rows) > h {
		pos := "▲▼ " + itoa(m.Selected+1) + "/" + itoa(len(rows))
		cv.Text(x+w-width(pos), y+h, pos, fg(cFaint), -1)
	}
}

func (m *Model) drawDetail(cv *Canvas, x, y, w, h int, r Row, scroll bool) {
	lines := []string{}
	for _, v := range r.Details {
		lines = append(lines, wrap(v, w)...)
	}
	off := 0
	if scroll {
		if m.Offset > max(0, len(lines)-h) {
			m.Offset = max(0, len(lines)-h)
		}
		off = m.Offset
	}
	for i := 0; i < h && off+i < len(lines); i++ {
		l := lines[off+i]
		switch {
		case strings.HasPrefix(l, "CACHED VIEW") || strings.HasPrefix(l, "LOCAL DRAFT"):
			cv.Text(x, y+i, l, fg(cWarn).bold(), w)
		case strings.HasSuffix(l, ":") && !strings.Contains(strings.TrimSuffix(l, ":"), ": "):
			cv.Text(x, y+i, l, fg(cAccent).bold(), w)
		case strings.Contains(l, ": ") && strings.Index(l, ": ") < 34:
			k := strings.Index(l, ": ")
			n := cv.Text(x, y+i, l[:k+1], fg(cMuted), w)
			cv.Text(x+n, y+i, l[k+1:], fg(cText), w-n)
		case strings.Contains(l, " → "):
			cx := x
			for p, part := range strings.Split(l, " → ") {
				if p > 0 {
					cx += cv.Text(cx, y+i, " → ", fg(cAccent), x+w-cx)
				}
				cx += cv.Text(cx, y+i, part, fg(cText), x+w-cx)
			}
		default:
			cv.Text(x, y+i, l, fg(cText), w)
		}
	}
	if len(lines) > off+h && h > 0 {
		more := " ↓ " + itoa(len(lines)-off-h) + " more "
		cv.Text(x+w-width(more), y+h, more, fg(cFaint), -1)
	}
}

// cards are the Overview summary strip; bars use braille cells for a
// dot-matrix look.
func (m *Model) cards(cv *Canvas, x, y, w int) {
	n := 3
	if w >= 100 {
		n = 4
	}
	cw := w / n
	up, down, warn, unk := m.counts()
	total := up + down + warn + unk
	card := func(i int, title string, big string, bigC Color, draw func(cx, cy, cw int)) {
		px := x + i*cw
		pw := cw
		if i == n-1 {
			pw = w - i*cw
		}
		ix, iy, iw, _ := m.panel(cv, px, y, pw, 4, title, "", false)
		cv.Text(ix, iy, big, fg(bigC).bold(), iw)
		draw(ix, iy+1, iw)
	}
	card(0, "CHECKS", itoa(total)+" configured", cText, func(cx, cy, cw int) {
		if total == 0 {
			cv.Text(cx, cy, "no checks in this view", fg(cMuted), cw)
			return
		}
		segs := []struct {
			n int
			c Color
		}{{up, cUp}, {warn, cWarn}, {down, cDown}, {unk, cFaint}}
		pos := 0
		for _, s := range segs {
			k := s.n * cw / total
			if s.n > 0 && k == 0 {
				k = 1
			}
			for j := 0; j < k && pos < cw; j++ {
				cv.Set(cx+pos, cy, '⣿', fg(s.c))
				pos++
			}
		}
		for ; pos < cw; pos++ {
			cv.Set(cx+pos, cy, '⣀', fg(cFaint))
		}
	})
	inc := len(m.Snapshot.Incidents)
	big, c := "none open", cUp
	if inc > 0 {
		big, c = itoa(inc)+" open", cDown
	}
	card(1, "INCIDENTS", big, c, func(cx, cy, cw int) {
		if inc > 0 {
			cv.Text(cx, cy, m.Snapshot.Incidents[0].Scope, fg(cMuted), cw)
		} else {
			cv.Text(cx, cy, "no inferred outage in this view", fg(cMuted), cw)
		}
	})
	live := 0
	for _, o := range m.Snapshot.Observers {
		if o.Health == "up" && !m.Cached() {
			live++
		}
	}
	card(2, "OBSERVERS", itoa(live)+"/"+itoa(len(m.Snapshot.Observers))+" live", cAccent2, func(cx, cy, cw int) {
		k := cx
		for _, o := range m.Snapshot.Observers {
			h := o.Health
			if m.Cached() {
				h = "unknown"
			}
			if k+2 > cx+cw {
				break
			}
			cv.Set(k, cy, healthGlyph(h), fg(healthColor(h)))
			k += 2
		}
	})
	if n == 4 {
		t := m.Snapshot.Topology
		card(3, "TOPOLOGY", itoa(len(t.Nodes))+" nodes · "+itoa(len(t.Links))+" links", cText, func(cx, cy, cw int) {
			cv.Text(cx, cy, itoa(len(t.Paths))+" paths · "+itoa(len(t.Endpoints))+" endpoints · "+itoa(len(t.Services))+" services", fg(cMuted), cw)
		})
	}
}

type keycap struct{ key, label, action string }

func (m *Model) footer(cv *Canvas, y int) {
	x := 1
	if m.Searching {
		x += cv.Text(x, y, "Filter: ", fg(cAccent).bold(), -1)
		x += cv.Text(x, y, m.Search, fg(cGlow), cv.W-x-20)
		cv.Text(x, y, "▏  Enter/Esc finish", fg(cMuted), -1)
		return
	}
	var keys []keycap
	if m.modal != nil {
		switch m.modal.kind {
		case "form":
			keys = []keycap{{"Tab/↓", "next", ""}, {"↑", "prev", ""}, {"←→", "choose", ""}, {"Space", "toggle", ""}, {"^S", "apply", "btn:save"}, {"Esc", "cancel", "btn:cancel"}}
		case "menu":
			keys = []keycap{{"↑↓", "select", ""}, {"Enter", "choose", ""}, {"Esc", "close", "esc"}}
		default:
			keys = []keycap{{"Enter", "ok", "enter"}, {"Esc", "close", "esc"}}
		}
	} else {
		if m.Search != "" {
			x += cv.Text(x, y, "Filter: "+m.Search+"  [Esc clear]  ", fg(cAccent), -1)
		}
		keys = []keycap{{"a", "Add", "a"}, {"e", "Edit", "e"}, {"d", "Delete", "d"}}
		if m.Edit != nil {
			keys = append(keys, keycap{"^S", "Save", "ctrl-s"}, keycap{"u", "Undo", "u"})
		}
		if m.Preview != nil {
			keys = append(keys, keycap{"x", "Scenario", "x"}, keycap{"D", "Debug", "D"})
		}
		keys = append(keys, keycap{"/", "Filter", "/"}, keycap{"v", "View", "v"})
		if m.Edit == nil {
			keys = append(keys, keycap{"r", "Refresh", "r"})
		}
		if m.Tab == tabTopology {
			keys = append(keys, keycap{"m", "Map", "m"}, keycap{"z", "Zoom", "z"})
		}
		keys = append(keys, keycap{"↵", "Details", "enter"}, keycap{"?", "About", "?"}, keycap{"q", "Quit", "q"})
	}
	for _, k := range keys {
		need := width(k.key) + width(k.label) + 4
		if x+need > cv.W {
			break
		}
		start := x
		cap := " " + k.key + " "
		if m.NoColor {
			cap = "[" + k.key + "]"
		}
		x += cv.Text(x, y, cap, fg(cGlow).Bg(cKeyBG).bold(), -1)
		x += cv.Text(x, y, " "+k.label+" ", fg(cMuted), -1)
		if k.action != "" {
			m.hit(start, y, x-start, "key:"+k.action)
		}
	}
}

// --- modals ------------------------------------------------------------------

func (m *Model) modalBox(cv *Canvas, w, h int, title string) (int, int, int, int) {
	w = min(w, cv.W-2)
	h = min(h, cv.H-2)
	x := (cv.W - w) / 2
	y := max(1, (cv.H-h)/2)
	bg := Style{BG: cPanelBG}
	cv.Fill(x, y, w, h, bg)
	cv.Box(x, y, w, h, fg(cAccent).Bg(cPanelBG))
	cv.Text(x+2, y, " "+title+" ", fg(cDeep).Bg(cAccent).bold(), w-4)
	return x + 2, y + 1, w - 4, h - 2
}

func (m *Model) button(cv *Canvas, x, y int, label, action string, focused bool) int {
	st := fg(cText).Bg(cKeyBG)
	if focused {
		st = fg(cDeep).Bg(cAccent).bold()
	}
	text := "[ " + label + " ]"
	if !m.NoColor {
		text = "  " + label + "  "
	}
	n := cv.Text(x, y, text, st, -1)
	m.hit(x, y, n, action)
	return n
}

func (m *Model) drawModal(cv *Canvas) {
	md := m.modal
	bg := Style{BG: cPanelBG}
	switch md.kind {
	case "info", "confirm":
		mw := width(md.title) + 8
		for _, l := range md.lines {
			mw = max(mw, width(l)+6)
		}
		x, y, w, h := m.modalBox(cv, mw, len(md.lines)+5, md.title)
		for i, l := range md.lines {
			if i >= h-2 {
				break
			}
			st := bg.Fg(cText)
			if strings.HasPrefix(l, "    ") {
				st = bg.Fg(cAccent).bold()
			}
			cv.Text(x, y+1+i, l, st, w)
		}
		by := y + h - 1
		if md.kind == "info" {
			m.button(cv, x+w-8, by, "OK", "btn:ok", true)
		} else {
			n := m.button(cv, x+w-24, by, "y Yes", "btn:yes", true)
			m.button(cv, x+w-24+n+2, by, "Esc No", "btn:no", false)
		}
	case "menu":
		mw, hint := 60, 0
		for _, it := range md.items {
			mw = max(mw, width(it.label)+12)
			if it.hint != "" {
				hint = 4
			}
		}
		if hint > 0 {
			mw = max(mw, 72)
		}
		x, y, w, h := m.modalBox(cv, mw, len(md.items)+4+hint, md.title)
		if hint > 0 && md.sel < len(md.items) {
			hy := y + len(md.items) + 2
			for i, l := range wrap(md.items[md.sel].hint, w-2) {
				if hy+i >= y+h {
					break
				}
				cv.Text(x+1, hy+i, l, bg.Fg(cAccent2), w-2)
			}
		}
		for i, it := range md.items {
			st := bg.Fg(cText)
			if i == md.sel {
				st = Style{FG: cGlow, BG: cSelBG}.bold()
				cv.Fill(x-1, y+1+i, w+2, 1, st)
				cv.Set(x-1, y+1+i, '▌', fg(cAccent).Bg(cSelBG))
			}
			cv.Text(x+1, y+1+i, " "+it.key+" ", fg(cGlow).Bg(cKeyBG).bold(), -1)
			cv.Text(x+5, y+1+i, it.label, st, w-6)
			m.hit(x-1, y+1+i, w+2, "menu:"+itoa(i))
		}
	case "form":
		m.drawForm(cv, md.form)
	}
}

func (m *Model) drawForm(cv *Canvas, f *Form) {
	n := len(f.fields)
	x, y, w, h := m.modalBox(cv, 86, n+8, f.title)
	bg := Style{BG: cPanelBG}
	visible := max(1, h-5)
	if f.focus < n {
		if f.focus < f.scroll {
			f.scroll = f.focus
		}
		if f.focus >= f.scroll+visible {
			f.scroll = f.focus - visible + 1
		}
	}
	labelW := 15
	vw := max(4, w-labelW-2)
	for i := 0; i < visible && f.scroll+i < n; i++ {
		j := f.scroll + i
		fd := f.fields[j]
		ry := y + 1 + i
		focused := j == f.focus
		ls := bg.Fg(cMuted)
		if focused {
			ls = bg.Fg(cAccent).bold()
		}
		lbl := fd.label
		cv.Text(x+labelW-width(lbl)-1, ry, lbl, ls, labelW)
		box := Style{FG: cText, BG: Mix(cPanelBG, cKeyBG, .6)}
		if focused {
			box = Style{FG: cGlow, BG: cKeyBG}
		}
		if fd.readonly {
			box = bg.Fg(cMuted)
		}
		vx := x + labelW + 1
		cv.Fill(vx, ry, vw, 1, box)
		switch fd.kind {
		case fChoice:
			v := fd.value
			if v == "" {
				v = "(none)"
			}
			cv.Text(vx, ry, "‹ "+v+" ›", box, vw)
		case fBool:
			mark := "[ ] no"
			if fd.value == "yes" {
				mark = "[x] yes"
			}
			cv.Text(vx, ry, mark, box, vw)
		default:
			r := []rune(fd.value)
			cur := min(max(fd.cursor, 0), len(r))
			start := 0
			for width(string(r[start:cur])) > vw-2 {
				start++
			}
			cv.Text(vx, ry, string(r[start:]), box, vw)
			if focused && !fd.readonly {
				cxp := vx + width(string(r[start:cur]))
				ch := ' '
				if cur < len(r) {
					ch = r[cur]
				}
				if m.NoColor {
					ch = '▏'
				}
				cv.Set(cxp, ry, ch, Style{FG: cDeep, BG: cAccent})
			}
			if fd.readonly {
				cv.Text(vx+vw-8, ry, "(fixed)", bg.Fg(cFaint), -1)
			}
		}
		m.hit(x, ry, w, "field:"+itoa(j))
	}
	if n > visible {
		pos := itoa(min(n, f.scroll+visible)) + "/" + itoa(n)
		cv.Text(x+w-width(pos), y, pos, bg.Fg(cFaint), -1)
	}
	hy := y + h - 3
	switch {
	case f.err != "":
		cv.Text(x, hy, "✖ "+f.err, bg.Fg(cDown).bold(), w)
	case f.note != "":
		cv.Text(x, hy, "ℹ "+f.note, bg.Fg(cAccent2), w)
	}
	if f.focus < n && f.fields[f.focus].hint != "" {
		cv.Text(x, hy+1, f.fields[f.focus].hint, bg.Fg(cMuted).italic(), w)
	}
	by := y + h - 1
	bx := x + w - 30
	bx += m.button(cv, bx, by, "Apply ^S", "btn:save", f.focus == n) + 2
	m.button(cv, bx, by, "Cancel Esc", "btn:cancel", f.focus == n+1)
}
