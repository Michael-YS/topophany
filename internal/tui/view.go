package tui

import (
	"path/filepath"
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

// title names the terminal window after the verdict, so a tab bar or a
// taskbar says whether anything is wrong while the UI is out of sight.
func (m *Model) title() string {
	t := ""
	if m.Edit != nil {
		t = "✎ " + filepath.Base(m.Edit.Path)
		if m.Edit.Dirty {
			t += " ●"
		}
	} else {
		word, _ := m.verdict()
		glyph, rest, _ := strings.Cut(word, " ")
		if m.Loading != "" {
			glyph = "◌" // the spinner turns too fast for a title
		}
		t = glyph + " " + strings.ToLower(rest)
		if p := m.Snapshot.Project; p != "" {
			t += " · " + p
		}
	}
	return ellipsis(secure.Text(t), 100) + " · Topophany"
}

func (m *Model) Render() []string {
	cv := m.draw()
	if cv == nil {
		return []string{fit("Topophany: resize terminal (30x10 minimum)", max(m.Width, 1))}
	}
	return cv.Lines(m.profile())
}

// draw paints one frame, or returns nil when the terminal is too small.
func (m *Model) draw() *Canvas {
	w, h := m.Width, m.Height
	m.hits = m.hits[:0]
	if w < 30 || h < 10 {
		return nil
	}
	cv := NewCanvas(w, h)
	cv.Hyperlinks = m.Hyperlinks
	if !m.Transparent {
		cv.Base = cInk
	}
	m.header(cv)
	m.tabBar(cv, 1)
	top := 3
	if m.Tab == tabOverview && !m.hasNews() && !m.FullMap {
		// The Overview's status card says everything the status row would;
		// there the row is kept only for notices and errors, and while the
		// map alone has the card's place.
		top = 2
	} else {
		m.statusBar(cv, 2)
	}
	m.body(cv, 0, top, w, h-1-top)
	m.footer(cv, h-1)
	if m.Debug {
		m.drawDebug(cv)
	}
	if m.modal != nil {
		cv.Each(func(s Style) Style {
			c, b := s.FG, s.BG
			if c == 0 {
				c = cText
			}
			if b == 0 {
				b = cInk
			}
			s = Style{FG: Mix(c, cInk, .66), BG: Mix(b, cInk, .66)}
			if m.Transparent && s.BG == cInk {
				s.BG = 0
			}
			return s
		})
		m.drawingModal = true
		m.drawModal(cv)
		m.drawingModal = false
	}
	return cv
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
	if m.FullMap && (m.Tab == tabTopology || m.Tab == tabOverview) {
		m.fullMap(cv, x, y, w, h, rows, sel)
		return
	}
	switch {
	case m.Tab == tabTopology:
		mode := mapNames[m.mapMode()]
		if w >= 120 {
			lw := min(max(w*40/100, 34), 84)
			mw := w - lw
			// In a tall panel, a geo or virtual map takes only the rows it
			// can fill and the evidence moves under it, leaving the list the
			// full height.
			mh := h
			if rows := m.mapRows(mw - 2); rows > 0 && h-rows-2 >= 10 {
				mh = rows + 2
			}
			ix, iy, iw, ih := m.panel(cv, x, y, mw, mh, title+" · "+mode, "m switch · z zoom", false)
			m.drawMap(cv, ix-1, iy, iw+2, ih, sel)
			if mh < h {
				ix, iy, iw, ih = m.panel(cv, x, y+mh, mw, h-mh, "EVIDENCE", "Enter expand", false)
				m.drawDetail(cv, ix, iy, iw, ih, sel, false)
				ix, iy, iw, ih = m.panel(cv, x+mw, y, lw, h, "NODES · LINKS", tag, true)
				m.drawList(cv, ix, iy, iw, ih, rows)
				return
			}
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
	case m.Tab == tabOverview:
		m.overview(cv, x, y, w, h, rows, sel, tag)
		return
	case m.Tab == tabServers:
		ix, iy, iw, ih := m.panel(cv, x, y, w, h, title, tag, true)
		m.drawServers(cv, ix, iy, iw, ih, rows)
		return
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

// mapNames are the maps as panel titles name them.
var mapNames = map[string]string{"virtual": "virtual geo", "geo": "geo map", "network": "network graph"}

// fullMap gives the whole body to the map, so its cards and labels have
// the room to say everything. The selection still moves with ↑↓, and the
// panel's foot names it.
func (m *Model) fullMap(cv *Canvas, x, y, w, h int, rows []Row, sel Row) {
	title := strings.ToUpper(tabs[m.Tab]) + " · " + mapNames[m.mapMode()]
	ix, iy, iw, ih := m.panel(cv, x, y, w, h, title, "m switch · z zoom · f panels", false)
	m.drawMap(cv, ix-1, iy, iw+2, ih, sel)
	if sel.Kind == "" || w < 30 {
		return
	}
	// Along the foot: the selection and its state on the left, where it
	// stands in the list on the right.
	fy := y + h - 1
	pos := " " + itoa(m.Selected+1) + "/" + itoa(len(rows)) + " "
	pill, ps := statePill(sel)
	rx := x + w - 2 - width(pos)
	cv.Text(rx, fy, pos, fg(cFaint), -1)
	if rx -= width(pill) + 1; rx-x > 24 {
		cv.Text(rx, fy, pill, ps, -1)
	} else {
		rx += width(pill) + 1
	}
	cx := x + 2
	room := rx - 2 - cx
	sub := "  " + sel.Kind + " · " + sel.ID
	if _, scope, ok := strings.Cut(sel.ID, "/"); ok && sel.Kind == "incident" {
		sub = "  incident · " + scope
	}
	name := " " + strings.TrimSpace(sel.Title)
	if width(name)+width(sub)+1 > room {
		sub = ""
	}
	n := cv.Text(cx, fy, ellipsis(name, room-1), fg(cGlow).bold(), room-1)
	n += cv.Text(cx+n, fy, sub+" ", fg(cFaint), room-n)
	m.hit(cx, fy, n, "key:enter")
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
			// The cursor goes where the text before it is shown, without
			// the controls Text drops.
			shown := func(from int) int { return width(secure.Text(string(r[from:cur]))) }
			start := 0
			for shown(start) > vw-2 {
				start++
			}
			cv.Text(vx, ry, string(r[start:]), box, vw)
			if focused && !fd.readonly {
				cxp := vx + shown(start)
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
