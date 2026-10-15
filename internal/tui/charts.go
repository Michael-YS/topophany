package tui

import (
	"math"
	"strconv"
)

// Charts of percentages on a fixed 0–100 scale, so a full disk looks full
// and two hosts compare at a glance. A reading's color is its headroom:
// teal while there is plenty, warming as it nears its limit, amber past it.

// loadColor is the color of a reading v against limit (0 for none, when
// only a full 100% is a limit).
func loadColor(v, limit float64) Color {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if v > limit {
		return cWarn
	}
	t := clamp((v/limit-.55)/.45, 0, 1)
	return Mix(cAccent, cWarn, t*t)
}

var sparks = []rune("▁▂▃▄▅▆▇█")

// braille bits of a cell's dots, left and right, from its foot up.
var dotBits = [2][4]rune{{0x40, 0x04, 0x02, 0x01}, {0x80, 0x20, 0x10, 0x08}}

// areaChart draws readings one per column as a braille area h rows tall:
// each column rises to its reading, half a column eased from the reading
// before, its ridge in the reading's color over a dimmer body. The limit
// runs across as a dotted line wherever the area is below it.
func areaChart(vals []float64, h int, limit float64, current bool) []vline {
	dots := 4 * h
	level := func(v float64) int { return max(1, int(math.Round(v/100*float64(dots)))) }
	lim := -1
	if limit > 0 && limit < 100 {
		lim = int(math.Round(limit / 100 * float64(dots)))
	}
	type col struct {
		top   [2]int // dots filled in each half, 0 for none
		color Color
	}
	cols := make([]col, len(vals))
	for i, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		c := loadColor(v, limit)
		if !current {
			c = Mix(c, cInk, .55)
		}
		left := v
		if i > 0 && !math.IsNaN(vals[i-1]) {
			left = (vals[i-1] + v) / 2
		}
		cols[i] = col{[2]int{level(left), level(v)}, c}
	}
	out := make([]vline, h)
	for row := range h {
		foot := (h - 1 - row) * 4 // the lowest dot this row holds
		line := vline{}
		put := func(s string, st Style) {
			if n := len(line); n > 0 && line[n-1].st == st {
				line[n-1].s += s
				return
			}
			line = append(line, span{s, st})
		}
		for _, c := range cols {
			bits := rune(0)
			ridge := false
			for half := range 2 {
				for k := range 4 {
					if foot+k < c.top[half] {
						bits |= dotBits[half][k]
					}
				}
				if t := c.top[half]; t > foot && t <= foot+4 {
					ridge = true
				}
			}
			switch {
			case bits != 0 && ridge:
				put(string(0x2800+bits), fg(c.color))
			case bits != 0:
				put(string(0x2800+bits), fg(Mix(c.color, cInk, .5)))
			case lim > foot && lim <= foot+4:
				put(string(0x2800+dotBits[0][lim-foot-1]), fg(Mix(cWarn, cInk, .55)))
			default:
				put(" ", Style{})
			}
		}
		out[row] = line
	}
	return out
}

// padNaN puts NaN before vals until there are n.
func padNaN(vals []float64, n int) []float64 {
	if len(vals) >= n {
		return vals[len(vals)-n:]
	}
	out := make([]float64, n-len(vals), n)
	for i := range out {
		out[i] = math.NaN()
	}
	return append(out, vals...)
}

// pct writes a percentage to digits after the point.
func pct(v float64, digits int) string {
	if math.IsNaN(v) {
		return "—"
	}
	return strconv.FormatFloat(v, 'f', digits, 64) + "%"
}

// capacity writes used of total bytes in the unit total reads best in:
// "7.8/16 GiB".
func capacity(used, total uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	u, scale := 0, 1.0
	for u < len(units)-1 && float64(total)/scale >= 1000 {
		u, scale = u+1, scale*1024
	}
	num := func(n uint64) string {
		f := float64(n) / scale
		if f < 10 && u > 0 {
			return strconv.FormatFloat(f, 'f', 1, 64)
		}
		return strconv.FormatFloat(math.Round(f), 'f', 0, 64)
	}
	return num(used) + "/" + num(total) + " " + units[u]
}

// tank draws readings one per column as columns of blocks h rows tall
// over a well, so a panel of them fills as far as its resource is used,
// each column in its reading's color; a slot with no reading stays empty.
func tank(vals []float64, h int, limit float64, current bool, well Color) []vline {
	eighths := 8 * h
	out := make([]vline, h)
	for row := range h {
		foot := (h - 1 - row) * 8 // the eighths below this row
		line := vline{}
		put := func(r rune, st Style) {
			if n := len(line); n > 0 && line[n-1].st == st {
				line[n-1].s += string(r)
				return
			}
			line = append(line, span{string(r), st})
		}
		for _, v := range vals {
			if math.IsNaN(v) {
				put(' ', Style{BG: well})
				continue
			}
			e := max(1, int(math.Round(v/100*float64(eighths))))
			c := loadColor(v, limit)
			if !current {
				c = Mix(c, cInk, .6)
			}
			if e <= foot {
				put(' ', Style{BG: well})
				continue
			}
			put(sparks[min(7, e-foot-1)], fg(Mix(c, well, .15)).Bg(well))
		}
		out[row] = line
	}
	return out
}
