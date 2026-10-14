package tui

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// The frame around every view: a header bar, the tab strip, a one-line health
// summary, and the key bar at the bottom.

func (m *Model) header(cv *Canvas) {
	w := cv.W
	on := func(c Color) Style { return fg(c).Bg(cInkHi) }
	cv.Fill(0, 0, w, 1, Style{BG: cInkHi})
	x := 1
	cv.Set(x, 0, '◉', on(cAccent).bold())
	x += 2
	for i, r := range "TOPOPHANY" {
		cv.Set(x+i, 0, r, on(Mix(cAccent, cAccent2, float64(i)/8)).bold())
	}
	x += 9
	view := m.Observer
	if view == "" {
		view = "all observers"
	}
	if m.Snapshot.Project != "" {
		x += cv.Text(x, 0, "  ╱  ", on(cFaint), w-x-1)
		x += cv.Text(x, 0, m.Snapshot.Project, on(cText).bold(), w-x-1)
	}
	x += cv.Text(x, 0, "   view: ", on(cMuted), w-x-1)
	x += cv.Text(x, 0, view, on(cAccent2), w-x-1)
	type part struct {
		s  string
		st Style
	}
	var right []part
	add := func(s string, c Color) { right = append(right, part{s, on(c)}) }
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
		add("   "+age(m.Now, m.Snapshot.GeneratedAt)+"  ", cFaint)
		switch {
		case m.Loading != "":
			add("◌ LOADING", cAccent2)
		case m.Cached():
			add("◌ CACHED", cWarn)
		default:
			// The live dot breathes with each repaint, never faster than data.
			add("● LIVE", Mix(cUp, cGlow, .5*float64(m.Now.Second()%2)))
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

// badge is what a tab counts beside its name: the entries in it that need a
// look, red when any is down and amber otherwise; 0 when none do. A cached
// view, a draft or a view still loading claims nothing.
func (m *Model) badge(tab int) (int, Color) {
	if m.Cached() || m.Edit != nil || m.Loading != "" {
		return 0, 0
	}
	s := m.Snapshot
	n, down := 0, false
	look := func(health string, affected bool) {
		switch {
		case health == "down":
			n, down = n+1, true
		case health == "degraded", affected:
			n++
		}
	}
	switch tab {
	case tabServers:
		known, servers := map[string]bool{}, map[string]bool{}
		for _, nd := range s.Topology.Nodes {
			known[nd.ID] = true
		}
		for _, nd := range s.Topology.Nodes {
			servers[nd.ID] = serverNode(nd, known)
		}
		for _, e := range s.Nodes {
			if servers[e.ID] {
				look(e.Health, e.Affected)
			}
		}
	case tabServices:
		for _, e := range s.Services {
			look(e.Health, e.Affected)
		}
	case tabPaths:
		for _, e := range s.Paths {
			look(e.Health, e.Affected)
		}
	case tabChecks:
		for _, c := range s.Checks {
			look(c.Health, false)
		}
	case tabIncidents:
		n, down = len(s.Incidents), len(s.Incidents) > 0
	case tabObservers:
		for _, o := range s.Observers {
			look(o.Health, o.Health != "up")
		}
	}
	if down {
		return n, cDown
	}
	return n, cWarn
}

// tabBar draws numbered tabs, each with the badge of what in it needs a
// look. When they do not fit, the gaps close, then a badge only tints its
// tab's number, then inactive tabs collapse to their number. Under NO_COLOR
// the active tab is bracketed.
func (m *Model) tabBar(cv *Canvas, y int) {
	type tab struct {
		name  string
		count int
		c     Color
	}
	ts := make([]tab, len(tabs))
	for i, name := range tabs {
		n, c := m.badge(i)
		ts[i] = tab{name, n, c}
	}
	// need is the width of the strip with or without counts and names.
	need := func(counts, short bool) int {
		total := 0
		for i, t := range ts {
			label := itoa(i + 1)
			if !short || i == m.Tab {
				label += " " + t.name
			}
			if counts && t.count > 0 {
				label += " " + itoa(t.count)
			}
			total += width(label) + 3
		}
		return total
	}
	gap, counts, short := 1, true, false
	switch room := cv.W - 1; {
	case need(true, false) <= room:
	case need(true, false)-len(tabs) <= room:
		gap = 0
	case need(false, false) <= room:
		counts = false
	case need(false, false)-len(tabs) <= room:
		gap, counts = 0, false
	default:
		counts, short = false, true
	}
	x := 1
	for i, t := range ts {
		num, name := itoa(i+1), t.name
		if short && i != m.Tab {
			name = ""
		}
		active := i == m.Tab
		start := x
		if m.NoColor {
			text := " " + strings.TrimSpace(num+" "+name) + " "
			if active {
				text = "[" + num + " " + name + "]"
			}
			if t.count > 0 && counts {
				text = strings.TrimSuffix(text, " ") + " " + itoa(t.count) + " "
			}
			x += cv.Text(x, y, text, Style{}, -1)
		} else {
			ns, ls, pad := fg(cFaint), fg(cMuted), Style{}
			bs := fg(t.c).bold()
			if t.count > 0 && !counts {
				ns = fg(t.c).bold() // no room to count: the number says it
			}
			if active {
				pad = Style{BG: cAccent}
				ns = fg(Mix(cInk, cAccent, .5)).Bg(cAccent).bold()
				ls = fg(cInk).Bg(cAccent).bold()
				bs = fg(Mix(t.c, cInk, .45)).Bg(cAccent).bold()
			}
			x += cv.Text(x, y, " ", pad, -1)
			x += cv.Text(x, y, num, ns, -1)
			if name != "" {
				x += cv.Text(x, y, " "+name, ls, -1)
			}
			if t.count > 0 && counts {
				x += cv.Text(x, y, " ", pad, -1)
				x += cv.Text(x, y, itoa(t.count), bs, -1)
			}
			x += cv.Text(x, y, " ", pad, -1)
		}
		m.hit(start, y, x-start, "tab:"+itoa(i))
		x += gap
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

func (m *Model) liveObservers() int {
	live := 0
	for _, o := range m.Snapshot.Observers {
		if o.Health == "up" && !m.Cached() {
			live++
		}
	}
	return live
}

// healthBar draws a proportional up/warn/down/unknown bar; every non-zero
// share keeps at least one cell so a single failure is never rounded away.
func (m *Model) healthBar(cv *Canvas, x, y, w int, up, warn, down, unk int, glyph rune) {
	total := up + warn + down + unk
	if w <= 0 {
		return
	}
	if total == 0 {
		for i := 0; i < w; i++ {
			cv.Set(x+i, y, glyph, fg(cLine))
		}
		return
	}
	segs := []struct {
		n int
		c Color
	}{{up, cUp}, {warn, cWarn}, {down, cDown}, {unk, cFaint}}
	cells := make([]int, len(segs))
	used := 0
	for i, s := range segs {
		if s.n > 0 {
			cells[i] = max(1, s.n*w/total)
			used += cells[i]
		}
	}
	// Trim or pad the largest share so the bar is exactly w cells.
	big := 0
	for i := range cells {
		if cells[i] > cells[big] {
			big = i
		}
	}
	cells[big] += w - used
	pos := 0
	for i, s := range segs {
		for k := 0; k < cells[i] && pos < w; k++ {
			cv.Set(x+pos, y, glyph, fg(s.c))
			pos++
		}
	}
}

// hasNews is a notice still on show, an error, or a request in flight.
func (m *Model) hasNews() bool {
	return m.notice != "" && m.Now.Sub(m.noticeAt) < 8e9 || m.Error != "" || m.Loading != ""
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
	if m.Loading != "" {
		cv.Text(x, y, "◌ "+m.Loading+"…", fg(cAccent2), cv.W-2)
		return
	}
	if m.Edit != nil {
		// A draft has no evidence to count; say what it declares instead.
		c := m.Edit.Config
		x += cv.Text(x, y, "DRAFT", fg(cFaint).bold(), -1) + 3
		for i, part := range []string{
			plural(len(c.Nodes), "node"), plural(len(c.Links), "link"), plural(len(c.Endpoints), "endpoint"),
			plural(len(c.Services), "service"), plural(len(c.Observers), "observer"), plural(len(c.Paths), "path"), plural(len(c.Checks), "check"),
		} {
			if x+5+width(part) > cv.W-1 {
				break
			}
			if i > 0 {
				x += cv.Text(x, y, "  ·  ", fg(cFaint), -1)
			}
			n, word, _ := strings.Cut(part, " ")
			x += cv.Text(x, y, n, fg(cText).bold(), -1)
			x += cv.Text(x, y, " "+word, fg(cMuted), max(0, cv.W-1-x))
		}
		return
	}
	up, down, warn, unk := m.counts()
	x += cv.Text(x, y, "CHECKS", fg(cFaint).bold(), -1) + 2
	if bw := min(24, cv.W/9); bw >= 6 && !m.NoColor {
		m.healthBar(cv, x, y, bw, up, warn, down, unk, '━')
		x += bw + 3
	}
	// Zero counts recede; anything that needs attention is lit and bold.
	count := func(g rune, c Color, n int, word string) {
		if x >= cv.W-6 {
			return
		}
		gs, ns := fg(cFaint), fg(cFaint)
		if n > 0 {
			gs, ns = fg(c), fg(c).bold()
			if c == cUp || c == cUnknown {
				ns = fg(cText).bold()
			}
		}
		cv.Set(x, y, g, gs)
		x += 2
		x += cv.Text(x, y, itoa(n), ns, -1)
		x += cv.Text(x, y, " "+word+"   ", fg(cMuted), -1)
	}
	count('●', cUp, up, "up")
	count('▲', cWarn, warn, "warning")
	count('✖', cDown, down, "down")
	count('○', cUnknown, unk, "unknown")
	if x < cv.W-16 {
		x += cv.Text(x, y, "│   ", fg(cLine), -1)
		count('⚑', cDown, len(m.Snapshot.Incidents), map[bool]string{true: "incident", false: "incidents"}[len(m.Snapshot.Incidents) == 1])
	}
	if x < cv.W-20 {
		x += cv.Text(x, y, "│   ", fg(cLine), -1)
		live, all := m.liveObservers(), len(m.Snapshot.Observers)
		c := cAccent2
		if live < all {
			c = cWarn
		}
		cv.Set(x, y, '◎', fg(c))
		x += 2
		x += cv.Text(x, y, itoa(live)+"/"+itoa(all), fg(cText).bold(), -1)
		cv.Text(x, y, " observers live", fg(cMuted), cv.W-x-1)
	}
}

// panel draws a rounded frame with a title and returns the inner rectangle.
func (m *Model) panel(cv *Canvas, x, y, w, h int, title, tag string, active bool) (int, int, int, int) {
	border, ts := fg(cLine), fg(cMuted).bold()
	if active {
		border, ts = fg(Mix(cLine, cAccent, .35)), fg(cAccent).bold()
	}
	cv.Box(x, y, w, h, border)
	if title != "" && w > 6 {
		n := cv.Text(x+2, y, " "+title+" ", ts, w-4)
		if tag != "" && x+2+n+width(tag)+3 < x+w {
			cv.Text(x+2+n, y, tag+" ", fg(cFaint), w-4-n)
		}
	}
	return x + 2, y + 1, max(0, w-4), max(0, h-2)
}

type keycap struct{ key, label, action string }

func (m *Model) footer(cv *Canvas, y int) {
	bar := Style{BG: cInkHi}
	cv.Fill(0, y, cv.W, 1, bar)
	x := 1
	if m.Searching {
		x += cv.Text(x, y, "Filter ", bar.Fg(cAccent).bold(), -1)
		x += cv.Text(x, y, m.Search, bar.Fg(cGlow), cv.W-x-20)
		cv.Text(x, y, "▏  Enter/Esc finish", bar.Fg(cMuted), -1)
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
			x += cv.Text(x, y, "Filter “"+m.Search+"”  Esc clear   ", bar.Fg(cAccent), -1)
		}
		// Keys in reading order, each with a rank: when the bar is too
		// narrow the highest rank goes first, a whole rank at a time, so
		// Details, Filter and Quit stay while the editor hints of a
		// read-only client give way together.
		type ranked struct {
			k    keycap
			rank int
		}
		var opts []ranked
		add := func(rank int, key, label, action string) {
			opts = append(opts, ranked{keycap{key, label, action}, rank})
		}
		mapShown := m.Tab == tabTopology || m.Tab == tabOverview && cv.W >= 90 && cv.H >= 25
		if m.Edit != nil {
			add(0, "a", "Add", "a")
			add(0, "e", "Edit", "e")
			add(0, "d", "Delete", "d")
			add(0, "^S", "Save", "ctrl-s")
			add(0, "u", "Undo", "u")
		}
		add(1, "↵", "Details", "enter")
		add(1, "/", "Filter", "/")
		add(2, "v", "View", "v")
		if m.Edit == nil {
			add(3, "r", "Refresh", "r")
		}
		if m.Preview != nil {
			add(2, "x", "Scenario", "x")
		}
		if mapShown {
			add(3, "m", "Map", "m")
			add(4, "z", "Zoom", "z")
		}
		if m.Edit == nil {
			add(5, "a", "Add", "a")
			add(5, "e", "Edit", "e")
			add(5, "d", "Delete", "d")
		}
		if m.Preview != nil {
			add(6, "D", "Debug", "D")
		}
		add(3, "?", "About", "?")
		add(0, "q", "Quit", "q")
		for {
			total, worst := 0, 0
			for _, o := range opts {
				total += width(o.k.key) + width(o.k.label) + 5
				worst = max(worst, o.rank)
			}
			if total-1 <= cv.W-x || worst == 0 {
				break
			}
			kept := opts[:0]
			for _, o := range opts {
				if o.rank != worst {
					kept = append(kept, o)
				}
			}
			opts = kept
		}
		for _, o := range opts {
			keys = append(keys, o.k)
		}
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
		x += cv.Text(x, y, cap, fg(cAccent).Bg(cKeyBG).bold(), -1)
		x += cv.Text(x, y, " "+k.label+"  ", bar.Fg(cMuted), -1)
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
	cv.Box(x, y, w, h, fg(Mix(cLine, cAccent, .6)).Bg(cPanelBG))
	cv.Text(x+2, y, " "+title+" ", fg(cInk).Bg(cAccent).bold(), w-4)
	return x + 2, y + 1, w - 4, h - 2
}

func (m *Model) button(cv *Canvas, x, y int, label, action string, focused bool) int {
	st := fg(cText).Bg(cKeyBG)
	if focused {
		st = fg(cInk).Bg(cAccent).bold()
	}
	text := "[ " + label + " ]"
	if !m.NoColor {
		text = "  " + label + "  "
	}
	n := cv.Text(x, y, text, st, -1)
	m.hit(x, y, n, action)
	return n
}
