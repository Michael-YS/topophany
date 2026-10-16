//go:build !nodevice

package tui

import (
	"strconv"
	"strings"
)

// Touch: the controls a device's screen offers in place of keys. A target
// is as tall as a finger needs, three rows (42 pt) wherever it can be, and
// every key the footer offers stays reachable as a control, so no screen
// and no pose loses an action. The look is the terminal's: rounded frames,
// the phosphor accent on ink, the selection's bar and tint.

// touchRows is how tall a touch target is, in rows: 42 pt.
const touchRows = 3

// touch is what the touch layouts keep between frames.
type touch struct {
	drawer  bool   // the sidebar lies open over the page
	zoom    int    // the map's magnification, an index into mapScales
	pan     [2]int // the window into the magnified map, set by a drag
	panned  bool   // a drag set pan; otherwise the window follows the selection
	top     int    // the touch list's first item shown
	seen    int    // the selection the list last brought into view, +1
	down    [2]int // where a finger went down, in the terminal's cells
	pressed bool   // a finger is down
	layout  string // the layout the last frame drew
	mapRect rect   // where the last frame drew the map, in the screen's cells
	origin  [2]int // where the simulator put the screen in the terminal
}

// mapScales are the map's magnifications, a pinch apart.
var mapScales = []float64{1, 1.5, 2, 3}

// tabIcons are the tabs' glyphs on a rail or a dock, in tab order.
var tabIcons = []rune{'◉', '◈', '▤', '▦', '⇢', '✓', '⚑', '◎', 'ℹ'}

// touchKey performs a touch control's action k (after "touch:"), and
// returns what the run loop should do, as Key does.
func (m *Model) touchKey(k string) string {
	switch k {
	case "drawer":
		m.touch.drawer = !m.touch.drawer
	case "close":
		m.touch.drawer = false
	case "zoom-in", "zoom-out":
		z := m.touch.zoom + map[bool]int{true: 1, false: -1}[k == "zoom-in"]
		m.touch.zoom = min(max(z, 0), len(mapScales)-1)
		if m.touch.zoom == 0 {
			m.touch.panned = false
		}
	case "back":
		m.Detail = false
		m.Offset = 0
	case "prev", "next":
		// The page before or after in the list, still open.
		if n := len(m.Rows()); n > 0 {
			m.Selected = min(max(m.Selected+map[bool]int{true: -1, false: 1}[k == "prev"], 0), n-1)
			m.Offset = 0
		}
	case "observers":
		m.observerMenu()
	case "more":
		m.touch.drawer = false
		m.moreMenu()
	case "filter-clear":
		m.Search = ""
		m.Selected, m.Offset, m.touch.top = 0, 0, 0
	case "settings":
		m.touch.drawer = false
		m.openPreferences()
	case "about":
		m.touch.drawer = false
		m.setTab(tabAbout)
	case "scenario":
		m.touch.drawer = false
		if m.Preview != nil {
			m.scenarioMenu()
		}
	default:
		if n, ok := strings.CutPrefix(k, "open:"); ok {
			// A tap on a row of a phone's list opens it, as lists on
			// phones do; a group folds instead.
			i, e := strconv.Atoi(n)
			if e != nil {
				return ""
			}
			m.Selected = i
			if !m.toggleGroup() {
				m.Detail = true
			}
			m.Offset = 0
		}
		if n, ok := strings.CutPrefix(k, "tab:"); ok {
			m.touch.drawer = false
			return m.Key("tab:" + n)
		}
		if mode, ok := strings.CutPrefix(k, "map:"); ok && contains(mapModes, mode) {
			m.MapMode = mode
			m.touch.panned = false
		}
	}
	return ""
}

// observerMenu lists the vantage points to view the network from; picking
// one asks for its view.
func (m *Model) observerMenu() {
	ids := []string{""}
	labels := []string{"All observers"}
	for _, o := range m.Snapshot.Topology.Observers {
		ids = append(ids, o.ID)
		name := o.Name
		if name == "" {
			name = o.ID
		}
		labels = append(labels, name)
	}
	m.choiceMenu("View from", labels, func(i int) {
		m.Observer = ids[i]
		m.Selected, m.Offset, m.touch.top = 0, 0, 0
	})
	m.modal.sel = max(0, indexOf(ids, m.Observer))
	pick := m.modal.pick
	m.modal.pick = func(i int) string {
		pick(i)
		return "observe"
	}
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// --- controls ---------------------------------------------------------------

// chip is a button a finger can hit: a label in a rounded frame three rows
// tall. on lights it as the current choice. It returns its width.
func (m *Model) chip(cv *Canvas, x, y int, label, act string, on bool) int {
	w := width(label) + 4
	border, text, bg := fg(cLine), fg(cText), Style{}
	if on {
		border, text, bg = fg(Mix(cLine, cAccent, .7)), fg(cGlow).bold(), Style{BG: cSelBG}
	}
	cv.Fill(x+1, y+1, w-2, 1, bg)
	cv.Box(x, y, w, touchRows, border)
	cv.Text(x+2, y+1, label, text.Merge(bg), w-4)
	if act != "" {
		m.hitArea(x, y, w, touchRows, act)
	}
	return w
}

// segment is one choice of a segmented control.
type segment struct{ label, act string }

// segmented draws choices side by side in one frame, three rows tall, the
// chosen one lit as the active tab is; it returns its width.
func (m *Model) segmented(cv *Canvas, x, y int, segs []segment, chosen int) int {
	w := segmentsWidth(segs)
	border := fg(cLine)
	cv.Fill(x, y, w, touchRows, Style{BG: cInk})
	cv.Box(x, y, w, touchRows, border)
	cx := x + 1
	for i, s := range segs {
		sw := width(s.label) + 2
		if i > 0 {
			cv.Set(cx-1, y, '┬', border)
			cv.Set(cx-1, y+1, '│', border)
			cv.Set(cx-1, y+2, '┴', border)
		}
		st := fg(cMuted)
		if i == chosen {
			st = fg(cInk).Bg(cAccent).bold()
			cv.Fill(cx, y+1, sw, 1, st)
		}
		cv.Text(cx+1, y+1, s.label, st, sw-1)
		m.hitArea(cx-1, y, sw+2, touchRows, s.act)
		cx += sw + 1
	}
	return w
}

// field is a text field three rows tall: what the filter holds, or what it
// would filter, with a control to clear it.
func (m *Model) field(cv *Canvas, x, y, w int, placeholder string) {
	if w < 12 {
		return
	}
	border := fg(cLine)
	if m.Searching {
		border = fg(Mix(cLine, cAccent, .7))
	}
	cv.Box(x, y, w, touchRows, border)
	cv.Text(x+2, y+1, "⌕", fg(cAccent), -1)
	switch {
	case m.Search != "" || m.Searching:
		n := cv.Text(x+4, y+1, m.Search, fg(cGlow), w-10)
		if m.Searching {
			cv.Set(x+4+n, y+1, '▏', fg(cAccent))
		}
		if m.Search != "" {
			cv.Text(x+w-4, y+1, "✕", fg(cMuted), -1)
			m.hitArea(x+w-6, y, 6, touchRows, "touch:filter-clear")
		}
	default:
		cv.Text(x+4, y+1, placeholder, fg(cFaint).italic(), w-6)
	}
	m.hitArea(x, y, w-6, touchRows, "key:/")
}

// appBar is the top bar of a device's screen: the wordmark, the project
// and how live the view is, pad cells in from x. The view and the scenario
// have chips of their own under it (viewChips).
func (m *Model) appBar(cv *Canvas, x, y, w, pad int) {
	cv.Fill(x, y, pad, 1, Style{BG: cInkHi})
	m.headerBar(cv, x+pad, y, w-pad, true)
}

// viewChips are chips for the observer the view is from and, in a
// preview, the scenario, three rows tall from y; the age of the snapshot
// goes to the right where there is room. It returns the rows it took.
func (m *Model) viewChips(cv *Canvas, x, y, w int) int {
	view := "All observers"
	for _, o := range m.Snapshot.Topology.Observers {
		if o.ID == m.Observer {
			view = o.Name
			if view == "" {
				view = o.ID
			}
		}
	}
	// Every other action is under ⋯ at the right end.
	room := w - 6
	m.chip(cv, x+w-5, y, "⋯", "touch:more", false)
	chips := []struct{ label, act string }{{"◎ " + view + " ▾", "touch:observers"}}
	if m.Preview != nil {
		chips = append(chips, struct{ label, act string }{"◆ " + m.scenario() + " ▾", "touch:scenario"})
	}
	cx := x
	for i, c := range chips {
		label := c.label
		left := x + room - cx
		if i == len(chips)-1 && width(label)+4 > left && left >= 14 {
			label = ellipsis(strings.TrimSuffix(label, " ▾"), left-6) + " ▾"
		}
		if width(label)+4 > left {
			break
		}
		cx += m.chip(cv, cx, y, label, c.act, false) + 1
	}
	return touchRows
}

// backBar is a pushed page's bar, as a phone's navigation bar is: a chip back
// to the list it came from, named after it, and where the page stands in
// that list, with chips to the one before and the one after.
func (m *Model) backBar(cv *Canvas, x, y, w int, of int) {
	m.chip(cv, x, y, "‹ "+tabs[m.Tab], "touch:back", false)
	if of < 2 || w < 40 {
		return
	}
	rx := x + w - 5
	m.chip(cv, rx, y, "▾", "touch:next", false)
	rx -= 6
	m.chip(cv, rx, y, "▴", "touch:prev", false)
	pos := itoa(m.Selected+1) + " of " + itoa(of)
	cv.Text(rx-1-width(pos), y+1, pos, fg(cFaint), -1)
}

// --- tabs -------------------------------------------------------------------

// tabCount is a tab's badge as a rail shows it: the count, kept to two
// digits.
func tabCount(n int) string {
	if n > 99 {
		return "99"
	}
	return itoa(n)
}

// tabRail draws the tabs one above the other in r, three rows each, the
// active one wearing the selection's bar and tint: with their names where
// the rail is wide enough (iPhone Duo's column), as glyphs and badges where
// it is not (a phone's sidebar, whose drawer names them). It returns the
// rows the tabs took.
func (m *Model) tabRail(cv *Canvas, r rect) int {
	named := r.w >= 11
	y := r.y
	for i := range tabs {
		if y+touchRows > r.bottom() {
			break
		}
		active := i == m.Tab
		bg := Style{}
		if active {
			bg = Style{BG: cSelBG}
			cv.Fill(r.x, y, r.w, touchRows, bg)
			for k := 0; k < touchRows; k++ {
				cv.Set(r.x, y+k, '▌', fg(cAccent).Bg(cSelBG))
			}
		}
		ic, nc := fg(cMuted).Merge(bg), fg(cMuted).Merge(bg)
		if active {
			ic, nc = fg(cAccent).Merge(bg).bold(), fg(cGlow).Merge(bg).bold()
		}
		n, c := m.badge(i)
		if named {
			// The glyph and the name, and under the name what needs a look:
			// failures marked as on the lists, warnings as theirs.
			cv.Set(r.x+1, y+1, tabIcons[i], ic)
			cv.Text(r.x+3, y+1, tabs[i], nc, r.w-3)
			if n > 0 {
				mark := "▲ "
				if c == cDown {
					mark = "✖ "
				}
				cv.Text(r.x+3, y+2, mark+tabCount(n), fg(c).Merge(bg).bold(), r.w-3)
			}
		} else {
			// The glyph, and beside it what needs a look, in its color.
			cv.Set(r.x+1, y+1, tabIcons[i], ic)
			if n > 0 {
				cv.Text(r.x+3, y+1, tabCount(n), fg(c).Merge(bg).bold(), r.w-3)
			}
		}
		m.hitArea(r.x, y, r.w, touchRows, "touch:tab:"+itoa(i))
		y += touchRows
	}
	return y - r.y
}

// tabDock draws the tabs side by side along the foot of the page, as an
// iPhone's tab bar does: a glyph over its name, three rows tall, the
// active one lit.
func (m *Model) tabDock(cv *Canvas, r rect) {
	cv.Fill(r.x, r.y, r.w, r.h, Style{BG: cInkHi})
	for k := r.x; k < r.right(); k++ {
		cv.Set(k, r.y, '▔', fg(cLine).Bg(cInkHi))
	}
	n := len(tabs)
	for i := range tabs {
		x0 := r.x + i*r.w/n
		x1 := r.x + (i+1)*r.w/n
		w := x1 - x0
		active := i == m.Tab
		st, is := fg(cMuted).Bg(cInkHi), fg(cMuted).Bg(cInkHi)
		if active {
			st, is = fg(cGlow).Bg(cSelBG).bold(), fg(cAccent).Bg(cSelBG).bold()
			cv.Fill(x0, r.y+1, w, r.h-1, Style{BG: cSelBG})
			for k := x0; k < x1; k++ {
				cv.Set(k, r.y, '▀', fg(cAccent).Bg(cInkHi))
			}
		}
		name := ellipsis(tabs[i], w-1)
		cv.Set(x0+(w-1)/2, r.y+1, tabIcons[i], is)
		if c, col := m.badge(i); c > 0 {
			cv.Text(x0+(w-1)/2+2, r.y+1, tabCount(c), fg(col).Merge(Style{BG: st.BG}).bold(), -1)
		}
		cv.Text(x0+(w-width(name))/2, r.y+2, name, st, w)
		m.hitArea(x0, r.y, w, r.h, "touch:tab:"+itoa(i))
	}
}

// touchTabs draws the tabs along one line three rows tall, as a tablet's tab
// bar does: each a glyph, its name and its badge, the active one in a lit
// frame. Where they do not all fit, the inactive ones give up their names.
// It returns the column after the last.
func (m *Model) touchTabs(cv *Canvas, x, y, w int) int {
	type item struct {
		label string
		count int
		c     Color
	}
	items := make([]item, len(tabs))
	need := func(short bool) int {
		total := 0
		for i, it := range items {
			label := string(tabIcons[i])
			if !short || i == m.Tab {
				label += " " + tabs[i]
			}
			items[i].label = label
			n := width(label) + 4
			if it.count > 0 {
				n += width(tabCount(it.count)) + 1
			}
			total += n + 1
		}
		return total
	}
	for i := range items {
		items[i].count, items[i].c = m.badge(i)
	}
	if need(false) > w {
		need(true)
	}
	cx := x
	for i, it := range items {
		iw := width(it.label) + 4
		if it.count > 0 {
			iw += width(tabCount(it.count)) + 1
		}
		if cx+iw > x+w {
			break
		}
		active := i == m.Tab
		ls := fg(cMuted)
		if active {
			cv.Fill(cx+1, y+1, iw-2, 1, Style{BG: cSelBG})
			cv.Box(cx, y, iw, touchRows, fg(Mix(cLine, cAccent, .7)))
			ls = fg(cGlow).Bg(cSelBG).bold()
		}
		n := cv.Text(cx+2, y+1, it.label, ls, -1)
		cv.Set(cx+2, y+1, tabIcons[i], ls.Fg(map[bool]Color{true: cAccent, false: cMuted}[active]))
		if it.count > 0 {
			cv.Text(cx+3+n, y+1, tabCount(it.count), fg(it.c).Merge(Style{BG: ls.BG}).bold(), -1)
		}
		m.hitArea(cx, y, iw, touchRows, "touch:tab:"+itoa(i))
		cx += iw + 1
	}
	return cx - 1
}

// drawer lays the sidebar open over the page: every tab by name with what
// in it needs a look, then where the view is from, the scenario and the
// settings, each a row a finger can hit.
func (m *Model) drawer(cv *Canvas, page rect) {
	w := min(page.w-6, 36)
	cv.Tint(page.x, page.y, page.w, page.h, func(s Style) Style {
		c, b := s.FG, s.BG
		if c == 0 {
			c = cText
		}
		if b == 0 {
			b = cInk
		}
		return Style{FG: Mix(c, cInk, .66), BG: Mix(b, cInk, .66)}
	})
	m.hitArea(page.x, page.y, page.w, page.h, "touch:close")
	x, y := page.x, page.y
	bg := Style{BG: cPanelBG}
	cv.Fill(x, y, w, page.h, bg)
	for k := 0; k < page.h; k++ {
		cv.Set(x+w, y+k, '│', fg(Mix(cLine, cAccent, .5)))
	}
	m.hitArea(x, y, w+1, page.h, "touch:none")
	cv.Set(x+2, y+1, '◉', bg.Fg(cAccent).bold())
	for i, r := range "TOPOPHANY" {
		cv.Set(x+4+i, y+1, r, bg.Fg(Mix(cAccent, cAccent2, float64(i)/8)).bold())
	}
	cv.Text(x+w-4, y+1, "✕", bg.Fg(cMuted), -1)
	m.hitArea(x+w-7, y, 7, touchRows, "touch:close")
	sub := m.Snapshot.Project
	if m.Server != "" {
		sub += " · " + m.Server
	}
	cv.Text(x+2, y+2, ellipsis(sub, w-4), bg.Fg(cFaint), w-4)
	row := func(yy int, icon rune, label, note string, nc Color, act string, active bool) {
		st, is := bg.Fg(cText), bg.Fg(cMuted)
		if active {
			cv.Fill(x, yy, w, touchRows, Style{BG: cSelBG})
			for k := 0; k < touchRows; k++ {
				cv.Set(x, yy+k, '▌', fg(cAccent).Bg(cSelBG))
			}
			st, is = fg(cGlow).Bg(cSelBG).bold(), fg(cAccent).Bg(cSelBG).bold()
		}
		cv.Set(x+2, yy+1, icon, is)
		room := w - 7 - width(note)
		cv.Text(x+4, yy+1, ellipsis(label, room), st, room)
		if note != "" {
			cv.Text(x+w-2-width(note), yy+1, note, Style{FG: nc, BG: st.BG}.bold(), -1)
		}
		m.hitArea(x, yy, w, touchRows, act)
	}
	y += 4
	for i := range tabs {
		if y+touchRows > page.bottom() {
			return
		}
		note, c := "", cFaint
		if n, nc := m.badge(i); n > 0 {
			note, c = itoa(n), nc
		}
		row(y, tabIcons[i], itoa(i+1)+"  "+tabs[i], note, c, "touch:tab:"+itoa(i), i == m.Tab)
		y += touchRows
	}
	for k := x + 2; k < x+w-2; k++ {
		cv.Set(k, y, '─', bg.Fg(cLine))
	}
	y++
	view := "all observers"
	if m.Observer != "" {
		view = m.Observer
	}
	extras := []struct {
		icon        rune
		label, note string
		act         string
	}{{'◎', "View: " + view, "›", "touch:observers"}}
	if m.Preview != nil {
		extras = append(extras, struct {
			icon        rune
			label, note string
			act         string
		}{'◆', "Scenario: " + m.scenario(), "›", "touch:scenario"})
	}
	extras = append(extras, struct {
		icon        rune
		label, note string
		act         string
	}{'≡', "Settings", "›", "touch:settings"})
	for _, e := range extras {
		if y+touchRows > page.bottom() {
			return
		}
		row(y, e.icon, e.label, e.note, cMuted, e.act, false)
		y += touchRows
	}
}

// --- lists ------------------------------------------------------------------

// touchList draws rows as a phone lists them: each three rows tall, its
// name and state over what it is, a hairline under it, the selection
// tinted with the accent's bar. It scrolls by whole items; a selection
// that moves comes into view. open makes a tap open the row rather than
// select it.
func (m *Model) touchList(cv *Canvas, x, y, w, h int, rows []Row, open bool) {
	if h <= 0 || w < 8 {
		return
	}
	if len(rows) == 0 {
		m.drawList(cv, x, y, w, h, rows)
		return
	}
	rows = m.logoRows(rows)
	n := max(1, h/touchRows)
	if m.touch.seen != m.Selected+1 {
		// The selection moved: bring it into view.
		if m.Selected < m.touch.top {
			m.touch.top = m.Selected
		}
		if m.Selected >= m.touch.top+n {
			m.touch.top = m.Selected - n + 1
		}
		m.touch.seen = m.Selected + 1
	}
	m.touch.top = min(max(m.touch.top, 0), max(0, len(rows)-n))
	groups := m.groupColors()
	for i := 0; i < n; i++ {
		j := m.touch.top + i
		if j >= len(rows) {
			break
		}
		r := rows[j]
		yy := y + i*touchRows
		selected := j == m.Selected
		base := Style{}
		if selected {
			base = Style{BG: cSelBG}
			cv.Fill(x-1, yy, w+2, 2, base)
			cv.Set(x-1, yy, '▌', fg(cAccent).Bg(cSelBG))
			cv.Set(x-1, yy+1, '▌', fg(cAccent).Bg(cSelBG))
		}
		word, ws := stateWord(r)
		right := w
		if word != "" {
			right -= width(word) + 1
			cv.Text(x+w-width(word), yy, word, base.Merge(ws), -1)
		}
		ts := base.Fg(cText).bold()
		if selected {
			ts = base.Fg(cGlow).bold()
		}
		if r.Kind == "group" {
			cv.Text(x, yy, ellipsis(r.Tree+r.Title, right), ts, right)
		} else {
			cv.Set(x, yy, healthGlyph(r.Health), base.Fg(healthColor(r.Health)).bold())
			shown := ellipsis(r.Tree+r.Title, right-2)
			cv.Text(x+2, yy, shown, ts, right-2)
			m.highlight(cv, x+2, yy, shown, base)
		}
		// Under the name, what it is: its kind, what it says of itself and
		// its columns, each once.
		parts := []span{}
		said := map[string]bool{"": true, "-": true, r.Kind: true}
		if r.Kind != "group" {
			parts = append(parts, span{r.Kind, base.Fg(cFaint)})
		}
		if !said[r.Meta] {
			parts = append(parts, span{" · ", base.Fg(cFaint)}, span{r.Meta, base.Fg(cMuted)})
			said[r.Meta] = true
		}
		for k, c := range r.Cols {
			if said[c] {
				continue
			}
			said[c] = true
			cs := base.Fg(cMuted)
			if k == 0 && r.Kind == "node" {
				if gc, ok := groups[c]; ok {
					cs = base.Fg(gc)
				}
			}
			if tc, ok := cellTone[c]; ok {
				cs = base.Fg(tc)
			}
			if len(parts) > 0 {
				parts = append(parts, span{" · ", base.Fg(cFaint)})
			}
			parts = append(parts, span{c, cs})
		}
		cx := x + 2
		if r.Kind == "group" {
			cx = x
		}
		for k, p := range parts {
			room := x + w - cx
			if room <= 0 || p.s == " · " && k+1 < len(parts) && room < 4 {
				break
			}
			cx += cv.Text(cx, yy+1, ellipsis(p.s, room), p.st, room)
			if width(p.s) > room {
				break // cut short: what follows would read as part of it
			}
		}
		if !selected && j+1 < len(rows) && i+1 < n {
			for k := x + 2; k < x+w; k++ {
				cv.Set(k, yy+2, '╌', fg(Mix(cLine, cInk, .35)))
			}
		}
		act := "row:" + itoa(j)
		if open {
			act = "touch:open:" + itoa(j)
		}
		m.hitArea(x-1, yy, w+2, touchRows, act)
	}
	if len(rows) > n {
		pos := " " + itoa(min(len(rows), m.touch.top+n)) + "/" + itoa(len(rows)) + " "
		cv.Text(x+w-width(pos), y+h, pos, fg(cFaint), -1)
		scrollThumb(cv, x+w+1, y, h, len(rows)*touchRows, m.touch.top*touchRows, fg(Mix(cLine, cAccent, .45)))
	}
}

// scrollList moves the touch list by n items, the selection where it was.
func (m *Model) scrollList(n int) {
	m.touch.top = max(0, m.touch.top+n)
	m.touch.seen = m.Selected + 1
}
