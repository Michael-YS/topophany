package tui

import (
	"strings"
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
	if !m.Transparent {
		cv.Base = cInk
	}
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
	return cv.Lines(m.profile())
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
		if w >= 120 {
			lw := min(max(w*40/100, 34), 84)
			mw := w - lw
			// A world-spanning network letterboxes in a tall panel; the map
			// then takes only the rows it can fill and the evidence moves
			// under it, leaving the list the full height.
			mh := h
			if m.mapMode() == "geo" {
				if need := m.geoRows(mw-2) + 2 + m.mapChrome(); h-need >= 10 {
					mh = need
				}
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
