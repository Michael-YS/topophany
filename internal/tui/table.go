package tui

import "strings"

// Lists are drawn as tables: a health glyph, the title (with its tree guide
// in the Topology hierarchy), aligned secondary columns, and a state column
// that stays empty for healthy rows so problems are the only lit words.

// stateWord is the state-column label of a row and its style; "" for rows
// that are simply up.
func stateWord(r Row) (string, Style) {
	switch r.Note {
	case "cached":
		return "cached", fg(cWarn)
	case "affected":
		return "affected", fg(cWarn)
	case "paused", "maintenance":
		return r.Note, fg(cMuted)
	}
	switch r.Health {
	case "down":
		return "DOWN", fg(cDown).bold()
	case "degraded":
		return "WARN", fg(cWarn).bold()
	case "up":
		return "", Style{}
	}
	return "unknown", fg(cFaint)
}

type tableLayout struct {
	titleW int
	cols   []int  // widths of the Row.Cols shown; dropped columns are absent
	right  []bool // numeric columns are right-aligned
	stateW int
}

const tableGap = 2

// layoutTable sizes columns from every row, not just the visible ones, so the
// table does not jitter while scrolling. When space is short the widest
// column gives way first; a column is dropped (rightmost first) only when
// every column is already at its minimum.
func layoutTable(rows []Row, heads []string, w int) tableLayout {
	l := tableLayout{}
	ncol := 0
	for _, r := range rows {
		l.titleW = max(l.titleW, width(r.Tree+r.Title))
		ncol = max(ncol, len(r.Cols))
		if s, _ := stateWord(r); s != "" {
			l.stateW = max(l.stateW, width(s))
		}
	}
	if len(heads) > 0 {
		l.titleW = max(l.titleW, width(heads[0]))
	}
	natTitle := l.titleW
	l.titleW = min(l.titleW, 44)
	pref := make([]int, ncol)
	natural := make([]int, ncol)
	l.right = make([]bool, ncol)
	for i := range pref {
		numeric := false
		for _, r := range rows {
			if i < len(r.Cols) && r.Cols[i] != "" {
				pref[i] = max(pref[i], width(r.Cols[i]))
				natural[i] = pref[i]
				v := r.Cols[i]
				numeric = v[0] >= '0' && v[0] <= '9' && (numeric || pref[i] == width(v))
			}
		}
		if pref[i] > 0 && i+1 < len(heads) {
			pref[i] = max(pref[i], min(width(heads[i+1]), 12))
		}
		pref[i] = min(pref[i], 28)
		l.right[i] = numeric
	}
	for len(pref) > 0 && pref[len(pref)-1] == 0 {
		pref = pref[:len(pref)-1]
	}
	title := l.titleW
	for n := len(pref); n >= 0; n-- {
		l.cols = append(l.cols[:0], pref[:n]...)
		l.titleW = title
		total := 2 + l.titleW
		for _, c := range l.cols {
			total += tableGap + c
		}
		if l.stateW > 0 {
			total += tableGap + l.stateW
		}
		for total > w {
			widest, at := l.titleW-12, -1
			for i, c := range l.cols {
				if c-6 > widest {
					widest, at = c-6, i
				}
			}
			if widest <= 0 {
				break
			}
			if at < 0 {
				l.titleW--
			} else {
				l.cols[at]--
			}
			total--
		}
		if total <= w || n == 0 {
			if total > w {
				l.titleW = max(4, l.titleW-(total-w))
			}
			// Spare room goes back to whatever was capped: long titles
			// first, then the rightmost columns (routes, addresses).
			spare := w - total
			grow := func(cur *int, want int) {
				if d := min(spare, want-*cur); d > 0 {
					*cur += d
					spare -= d
				}
			}
			grow(&l.titleW, natTitle)
			for i := len(l.cols) - 1; i >= 0; i-- {
				grow(&l.cols[i], natural[i])
			}
			break
		}
	}
	l.right = l.right[:len(l.cols)]
	return l
}

func (m *Model) drawList(cv *Canvas, x, y, w, h int, rows []Row) {
	if h <= 0 {
		return
	}
	if len(rows) == 0 {
		msg := "No matching entries / no current evidence."
		if m.Edit != nil && m.Search == "" {
			msg = "Nothing here yet — press a to add."
		}
		cv.Text(x, y, msg, fg(cMuted).italic(), w)
		return
	}
	heads := m.tabColumns()
	lay := layoutTable(rows, heads, w)
	if len(heads) > 0 && h >= 6 {
		hs := fg(cFaint).bold()
		cx := x + 2
		cv.Text(cx, y, heads[0], hs, lay.titleW)
		cx += lay.titleW
		for i, cw := range lay.cols {
			cx += tableGap
			if i+1 < len(heads) {
				head := heads[i+1]
				if lay.right[i] {
					head = strings.Repeat(" ", max(0, cw-width(head))) + head
				}
				cv.Text(cx, y, head, hs, cw)
			}
			cx += cw
		}
		y++
		h--
	}
	if m.Selected < m.Offset {
		m.Offset = m.Selected
	}
	if m.Selected >= m.Offset+h {
		m.Offset = m.Selected - h + 1
	}
	groups := m.groupColors()
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
			mark := ' '
			if selected {
				mark = '›'
			}
			cv.Set(cx-1, y+i, mark, base)
		}
		cv.Set(cx, y+i, healthGlyph(r.Health), base.Fg(healthColor(r.Health)).bold())
		cx += 2
		tw := lay.titleW
		if r.Tree != "" {
			n := cv.Text(cx, y+i, r.Tree, base.Fg(cFaint), tw)
			cx += n
			tw -= n
		}
		ts := base.Fg(cText)
		switch {
		case selected:
			ts = base.Fg(cGlow).bold()
		case r.Kind == "endpoint" || r.Kind == "link":
			ts = base.Fg(Mix(cText, cMuted, .55))
		case r.Kind == "node" && r.Tree == "":
			ts = ts.bold()
		}
		cv.Text(cx, y+i, r.Title, ts, tw)
		cx += tw
		for k, cw := range lay.cols {
			cx += tableGap
			if k < len(r.Cols) {
				cs := base.Fg(cMuted)
				if k == 0 && r.Kind == "node" {
					if c, ok := groups[r.Cols[0]]; ok {
						cs = base.Fg(c)
					}
				}
				if r.Kind == "endpoint" || r.Kind == "link" {
					cs = base.Fg(Mix(cMuted, cFaint, .35))
				}
				v := r.Cols[k]
				if lay.right[k] {
					v = strings.Repeat(" ", max(0, cw-width(v))) + v
				}
				cv.Text(cx, y+i, v, cs, cw)
			}
			cx += cw
		}
		if word, st := stateWord(r); word != "" {
			cv.Text(x+w-width(word), y+i, word, base.Merge(st), -1)
		}
		m.hit(x-1, y+i, w+2, "row:"+itoa(j))
	}
	if len(rows) > h {
		pos := " " + itoa(m.Selected+1) + "/" + itoa(len(rows)) + " "
		cv.Text(x+w-width(pos), y+h, pos, fg(cFaint), -1)
		// A thumb on the right border shows where the window sits.
		track := h
		thumb := max(1, track*h/len(rows))
		top := (track - thumb) * m.Offset / max(1, len(rows)-h)
		for k := 0; k < thumb; k++ {
			cv.Set(x+w+1, y+top+k, '┃', fg(Mix(cLine, cAccent, .45)))
		}
	}
}

// groupColors assigns each node group the hue it has on the maps.
func (m *Model) groupColors() map[string]Color {
	out := map[string]Color{}
	for _, n := range m.Snapshot.Topology.Nodes {
		if _, ok := out[n.Group]; !ok {
			out[n.Group] = groupHues[len(out)%len(groupHues)]
		}
	}
	return out
}
