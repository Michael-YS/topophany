package tui

import (
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Color is 0 for the terminal default, otherwise 0xFF000000|RGB.
type Color uint32

func Hex(v uint32) Color { return Color(0xFF000000 | v&0xFFFFFF) }
func (c Color) rgb() (int, int, int) {
	return int(c>>16) & 255, int(c>>8) & 255, int(c) & 255
}

// Mix linearly interpolates two non-default colors; t=0 gives a.
func Mix(a, b Color, t float64) Color {
	if a == 0 || b == 0 {
		if t < .5 {
			return a
		}
		return b
	}
	t = clamp(t, 0, 1)
	ar, ag, ab := a.rgb()
	br, bg, bb := b.rgb()
	l := func(x, y int) uint32 { return uint32(float64(x) + (float64(y)-float64(x))*t + .5) }
	return Hex(l(ar, br)<<16 | l(ag, bg)<<8 | l(ab, bb))
}

const (
	Bold uint8 = 1 << iota
	Faint
	Italic
	Underline
	Reverse
)

type Style struct {
	FG, BG Color
	Attr   uint8
}

func (s Style) Fg(c Color) Style   { s.FG = c; return s }
func (s Style) Bg(c Color) Style   { s.BG = c; return s }
func (s Style) With(a uint8) Style { s.Attr |= a; return s }
func fg(c Color) Style             { return Style{FG: c} }
func (s Style) bold() Style        { return s.With(Bold) }
func (s Style) plain() bool        { return s == Style{} }
func (s Style) italic() Style      { return s.With(Italic) }

// Merge overlays o's colors and attributes; unset colors keep s's.
func (s Style) Merge(o Style) Style {
	if o.FG != 0 {
		s.FG = o.FG
	}
	if o.BG != 0 {
		s.BG = o.BG
	}
	s.Attr |= o.Attr
	return s
}

type cell struct {
	ch   string
	st   Style
	cont bool   // right half of a wide rune
	link uint16 // 1 + index into the canvas's links; 0 for none
}

// Canvas is a fixed grid of terminal cells. Every rendered line is exactly W
// columns wide; untrusted text is stripped of control characters on entry.
// Base, when set, is the background of every cell that does not set its own.
// Hyperlinks, when set, writes the links on cells as OSC 8 hyperlinks.
type Canvas struct {
	W, H       int
	Base       Color
	Hyperlinks bool
	cells      []cell
	links      []string
}

func NewCanvas(w, h int) *Canvas {
	c := &Canvas{W: max(w, 0), H: max(h, 0)}
	c.cells = make([]cell, c.W*c.H)
	for i := range c.cells {
		c.cells[i].ch = " "
	}
	return c
}
func (c *Canvas) in(x, y int) bool  { return x >= 0 && y >= 0 && x < c.W && y < c.H }
func (c *Canvas) at(x, y int) *cell { return &c.cells[y*c.W+x] }

// detach clears any wide rune that overlaps column x.
func (c *Canvas) detach(x, y int) {
	if !c.in(x, y) {
		return
	}
	p := c.at(x, y)
	if p.cont && x > 0 {
		h := c.at(x-1, y)
		h.ch = " "
		p.cont = false
		p.ch = " "
	}
	if x+1 < c.W && c.at(x+1, y).cont {
		n := c.at(x+1, y)
		n.cont = false
		n.ch = " "
	}
}

// put writes one cell. A style without a background keeps the background
// already painted there, so text and dots composite over panels and maps.
func (c *Canvas) put(x, y int, ch string, w int, st Style) {
	if !c.in(x, y) {
		return
	}
	if w == 2 && x+1 >= c.W {
		ch, w = " ", 1
	}
	if st.BG == 0 {
		st.BG = c.at(x, y).st.BG
	}
	c.detach(x, y)
	*c.at(x, y) = cell{ch: ch, st: st}
	if w == 2 {
		c.detach(x+1, y)
		*c.at(x+1, y) = cell{st: st, cont: true}
	}
}

// Set writes one rune; it is the primitive used for glyphs and dot art.
func (c *Canvas) Set(x, y int, r rune, st Style) { c.put(x, y, string(r), runeWidth(r), st) }

// Text writes s clipped to maxW columns (maxW<0: to the right edge) and
// returns the number of columns written.
func (c *Canvas) Text(x, y int, s string, st Style, maxW int) int {
	if y < 0 || y >= c.H {
		return 0
	}
	if maxW < 0 || x+maxW > c.W {
		maxW = c.W - x
	}
	s = secure.Text(s)
	n := 0
	lastX := -1
	for _, r := range s {
		k := runeWidth(r)
		if k == 0 {
			if lastX >= 0 && c.in(lastX, y) {
				c.at(lastX, y).ch += string(r)
			}
			continue
		}
		if n+k > maxW {
			break
		}
		if x+n >= 0 {
			c.put(x+n, y, string(r), k, st)
			lastX = x + n
		}
		n += k
	}
	return n
}

// Pad writes s and fills the remainder of w columns with st.
func (c *Canvas) Pad(x, y int, s string, st Style, w int) {
	n := c.Text(x, y, s, st, w)
	c.Fill(x+n, y, w-n, 1, st)
}
func (c *Canvas) Fill(x, y, w, h int, st Style) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			c.put(i, j, " ", 1, st)
		}
	}
}
func (c *Canvas) Box(x, y, w, h int, st Style) {
	if w < 2 || h < 2 {
		return
	}
	for i := x + 1; i < x+w-1; i++ {
		c.Set(i, y, '─', st)
		c.Set(i, y+h-1, '─', st)
	}
	for j := y + 1; j < y+h-1; j++ {
		c.Set(x, j, '│', st)
		c.Set(x+w-1, j, '│', st)
	}
	c.Set(x, y, '╭', st)
	c.Set(x+w-1, y, '╮', st)
	c.Set(x, y+h-1, '╰', st)
	c.Set(x+w-1, y+h-1, '╯', st)
}

// Link makes the w cells from (x,y) a hyperlink to url, which terminals
// that understand OSC 8 open on a click; drawing over a cell unlinks it.
func (c *Canvas) Link(x, y, w int, url string) {
	k := 0
	for i, u := range c.links {
		if u == url {
			k = i + 1
		}
	}
	if k == 0 {
		c.links = append(c.links, url)
		k = len(c.links)
	}
	for i := max(x, 0); i < x+w && i < c.W; i++ {
		if c.in(i, y) {
			c.at(i, y).link = uint16(k)
		}
	}
}

// url is the target of a cell's link, "" for none.
func (c *Canvas) url(p *cell) string {
	if p.link == 0 {
		return ""
	}
	return c.links[p.link-1]
}

// Each applies f to the style of every cell, e.g. to dim a modal background.
func (c *Canvas) Each(f func(Style) Style) {
	for i := range c.cells {
		c.cells[i].st = f(c.cells[i].st)
	}
}

const (
	colorTrue = iota
	color256
	colorNone
)

func sgr(b *strings.Builder, s Style, profile int) {
	b.WriteString("\x1b[0")
	for i, code := range []string{"1", "2", "3", "4", "7"} {
		if s.Attr&(1<<i) != 0 {
			b.WriteString(";" + code)
		}
	}
	color := func(c Color, base string) {
		if c == 0 {
			return
		}
		r, g, bl := c.rgb()
		if profile == color256 {
			b.WriteString(";" + base + ";5;" + strconv.Itoa(xterm256(r, g, bl)))
			return
		}
		b.WriteString(";" + base + ";2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(bl))
	}
	color(s.FG, "38")
	color(s.BG, "48")
	b.WriteString("m")
}

// sgrDelta moves the terminal from style cur to next writing only the
// colors that changed; anything that must be switched off takes a reset.
func sgrDelta(b *strings.Builder, cur, next Style, profile int) {
	if cur.Attr != next.Attr || cur.FG != 0 && next.FG == 0 || cur.BG != 0 && next.BG == 0 {
		sgr(b, next, profile)
		return
	}
	b.WriteString("\x1b[")
	sep := ""
	color := func(c Color, base string) {
		r, g, bl := c.rgb()
		b.WriteString(sep)
		sep = ";"
		if profile == color256 {
			b.WriteString(base + ";5;" + strconv.Itoa(xterm256(r, g, bl)))
			return
		}
		b.WriteString(base + ";2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(bl))
	}
	if next.FG != cur.FG {
		color(next.FG, "38")
	}
	if next.BG != cur.BG {
		color(next.BG, "48")
	}
	b.WriteString("m")
}

func xterm256(r, g, b int) int {
	q := func(v int) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return (v - 35) / 40
	}
	lv := []int{0, 95, 135, 175, 215, 255}
	cr, cg, cb := q(r), q(g), q(b)
	d := func(x, y, z int) int { return (x-r)*(x-r) + (y-g)*(y-g) + (z-b)*(z-b) }
	cube := 16 + 36*cr + 6*cg + cb
	avg := (r + g + b) / 3
	gi := min(23, max(0, (avg-3)/10))
	gv := 8 + gi*10
	if d(gv, gv, gv) < d(lv[cr], lv[cg], lv[cb]) {
		return 232 + gi
	}
	return cube
}

const visibleOnBlank = Underline | Reverse

// Lines serializes the canvas. Styles are emitted only on change and only
// for what changed (a blank cell only needs its background), and each line
// ends with a reset so a line can be repainted independently.
func (c *Canvas) Lines(profile int) []string {
	out := make([]string, c.H)
	var b strings.Builder
	for y := 0; y < c.H; y++ {
		b.Reset()
		c.span(&b, y, 0, c.W, profile)
		out[y] = b.String()
	}
	return out
}

// span writes the cells from column a up to e of row y, starting from and
// returning to the terminal's default style, outside any link.
func (c *Canvas) span(b *strings.Builder, y, a, e, profile int) {
	cur := Style{}
	styled := false
	link := ""
	for x := a; x < e; x++ {
		p := c.at(x, y)
		if p.cont {
			continue
		}
		if u := c.url(p); c.Hyperlinks && u != link {
			b.WriteString("\x1b]8;;" + u + "\x1b\\")
			link = u
		}
		st := p.st
		if st.BG == 0 {
			st.BG = c.Base
		}
		if p.ch == " " && (st.Attr|cur.Attr)&visibleOnBlank == 0 {
			st.FG, st.Attr = cur.FG, cur.Attr
		}
		if profile != colorNone && (st != cur || !styled && !st.plain()) {
			switch {
			case st.plain():
				b.WriteString("\x1b[0m")
			case !styled:
				sgr(b, st, profile)
			default:
				sgrDelta(b, cur, st, profile)
			}
			cur = st
			styled = true
		}
		b.WriteString(p.ch)
	}
	if link != "" {
		b.WriteString("\x1b]8;;\x1b\\")
	}
	if styled && !cur.plain() {
		b.WriteString("\x1b[0m")
	}
}

// same reports whether cell (x,y) looks the same on both canvases; a blank
// shows only its background, so its foreground does not count.
func (c *Canvas) same(prev *Canvas, x, y int) bool {
	p, q := c.at(x, y), prev.at(x, y)
	if p.ch != q.ch || p.cont != q.cont || c.url(p) != prev.url(q) {
		return false
	}
	if p.ch == " " && (p.st.Attr|q.st.Attr)&visibleOnBlank == 0 {
		return p.st.BG == q.st.BG
	}
	return p.st == q.st
}

// diffGap is how many unchanged cells a run of changes may span before it
// is cheaper to move the cursor past them than to write them again.
const diffGap = 12

// Diff serializes what changed since prev: each run of changed cells after
// a cursor move to where it starts, so a glint moving along a wire costs a
// few bytes rather than its whole line. The right half of a wide rune
// carries its style, so it changes only with the rune and a run never
// splits one. Without a previous canvas of the same size and base, every
// line is written.
func (c *Canvas) Diff(prev *Canvas, profile int) string {
	var b strings.Builder
	full := prev == nil || prev.W != c.W || prev.H != c.H || prev.Base != c.Base || prev.Hyperlinks != c.Hyperlinks
	for y := 0; y < c.H; y++ {
		if full {
			b.WriteString("\x1b[" + strconv.Itoa(y+1) + ";1H")
			c.span(&b, y, 0, c.W, profile)
			continue
		}
		for x := 0; x < c.W; {
			if c.same(prev, x, y) {
				x++
				continue
			}
			a, e := x, x+1
			for x = e; x < c.W && x-e < diffGap; x++ {
				if !c.same(prev, x, y) {
					e = x + 1
				}
			}
			b.WriteString("\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(a+1) + "H")
			c.span(&b, y, a, e, profile)
		}
	}
	return b.String()
}
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
