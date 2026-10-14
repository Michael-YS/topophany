package tui

import "math"

// Network edges are wires: box-drawing lines that leave a chip on the right,
// turn once in the gap before the next column and enter the next chip from
// the left. Every cell remembers which way each edge leaves it, so an edge's
// turns become rounded corners, edges from one source join with tees, and
// edges that only pass each other cross with a bridge (the horizontal line
// is drawn over the vertical one).

const (
	wN uint8 = 1 << iota
	wE
	wS
	wW
)

type wireMark struct {
	edge int
	dirs uint8
	c    Color
	pri  int8
	dash int // 0 solid, 1 dotted (hosted in), 2 dashed (declared path)
}

type wires struct {
	w, h  int
	marks map[int][]wireMark
}

func newWires(w, h int) *wires { return &wires{w: w, h: h, marks: map[int][]wireMark{}} }

func (ws *wires) mark(x, y, edge int, dirs uint8, c Color, pri int8, dash int) {
	if x < 0 || y < 0 || x >= ws.w || y >= ws.h {
		return
	}
	k := y*ws.w + x
	for i, mk := range ws.marks[k] {
		if mk.edge == edge {
			ws.marks[k][i].dirs |= dirs
			return
		}
	}
	ws.marks[k] = append(ws.marks[k], wireMark{edge, dirs, c, pri, dash})
}

// route marks the orthogonal polyline through pts and returns its cells in
// order, for the flow pulse.
func (ws *wires) route(pts [][2]int, edge int, c Color, pri int8, dash int) [][2]int {
	cells := [][2]int{}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		dx, dy, fwd, back := 0, 0, uint8(0), uint8(0)
		switch {
		case a[1] == b[1] && b[0] > a[0]:
			dx, fwd, back = 1, wE, wW
		case a[1] == b[1] && b[0] < a[0]:
			dx, fwd, back = -1, wW, wE
		case a[0] == b[0] && b[1] > a[1]:
			dy, fwd, back = 1, wS, wN
		case a[0] == b[0] && b[1] < a[1]:
			dy, fwd, back = -1, wN, wS
		default:
			continue
		}
		for x, y := a[0], a[1]; x != b[0] || y != b[1]; x, y = x+dx, y+dy {
			ws.mark(x, y, edge, fwd, c, pri, dash)
			ws.mark(x+dx, y+dy, edge, back, c, pri, dash)
			cells = append(cells, [2]int{x, y})
		}
	}
	if len(pts) > 0 {
		cells = append(cells, pts[len(pts)-1])
	}
	return cells
}

var wireGlyph = [16]rune{
	wE: '─', wW: '─', wE | wW: '─', wN: '│', wS: '│', wN | wS: '│',
	wE | wS: '╭', wW | wS: '╮', wN | wE: '╰', wN | wW: '╯',
	wE | wW | wS: '┬', wE | wW | wN: '┴', wN | wS | wE: '├', wN | wS | wW: '┤',
	wN | wE | wS | wW: '┼',
}

// top is the mark a cell shows: the edge needing the most attention, or,
// where two edges merely cross, the horizontal one; dirs are the directions
// the drawn glyph connects.
func (ws *wires) top(x, y int) (wireMark, uint8, bool) {
	marks := ws.marks[y*ws.w+x]
	if len(marks) == 0 {
		return wireMark{}, 0, false
	}
	top, dirs := marks[0], uint8(0)
	horiz, vert, turns := -1, -1, false
	for i, mk := range marks {
		dirs |= mk.dirs
		if mk.pri > top.pri {
			top = mk
		}
		switch {
		case mk.dirs&(wN|wS) == 0:
			if horiz < 0 || mk.pri > marks[horiz].pri {
				horiz = i
			}
		case mk.dirs&(wE|wW) == 0:
			if vert < 0 || mk.pri > marks[vert].pri {
				vert = i
			}
		default:
			turns = true
		}
	}
	if horiz >= 0 && vert >= 0 && !turns {
		return marks[horiz], wE | wW, true
	}
	return top, dirs, true
}

func (ws *wires) draw(cv *Canvas, ox, oy int) {
	for k := range ws.marks {
		x, y := k%ws.w, k/ws.w
		mk, dirs, _ := ws.top(x, y)
		r := wireGlyph[dirs]
		switch dirs {
		case wE, wW, wE | wW:
			r = [3]rune{'─', '┈', '╌'}[mk.dash]
		case wN, wS, wN | wS:
			r = [3]rune{'│', '┊', '╎'}[mk.dash]
		}
		cv.Set(ox+x, oy+y, r, fg(mk.c))
	}
}

// glint runs a short highlight along an edge's cells wherever its wire is the
// one shown, so motion itself means "getting through".
func (ws *wires) glint(cv *Canvas, ox, oy int, cells [][2]int, edge int, id string, phase float64) {
	n := len(cells)
	if n < 6 {
		return
	}
	head := int(math.Mod(phase*12+float64(hash32(id)%97), float64(n+18)))
	for t := 0; t < 2; t++ {
		i := head - t
		if i < 0 || i >= n {
			continue
		}
		px, py := cells[i][0], cells[i][1]
		if mk, _, ok := ws.top(px, py); ok && mk.edge == edge && cv.in(ox+px, oy+py) {
			cl := cv.at(ox+px, oy+py)
			cv.put(ox+px, oy+py, cl.ch, 1, cl.st.Fg(Mix(Mix(cUp, cGlow, .45), mk.c, float64(t)*.5)))
		}
	}
}
