package tui

import (
	_ "embed"
	"math"
)

// Dots is a braille sub-cell raster: each terminal cell holds 2x4 dots, which
// gives the dot-matrix look of the map and the About globe.
type Dots struct {
	W, H int // cells
	bits []uint8
	col  []Color
	pri  []int8
}

var brailleBit = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

func NewDots(w, h int) *Dots {
	w, h = max(w, 0), max(h, 0)
	return &Dots{W: w, H: h, bits: make([]uint8, w*h), col: make([]Color, w*h), pri: make([]int8, w*h)}
}

// Set lights dot (px,py). A cell shows only its highest-priority layer, so a
// route crossing land stays a clean line instead of merging with the texture.
func (d *Dots) Set(px, py int, c Color, pri int8) {
	if px < 0 || py < 0 || px >= d.W*2 || py >= d.H*4 {
		return
	}
	i := (py/4)*d.W + px/2
	bit := brailleBit[py%4][px%2]
	switch {
	case d.bits[i] == 0 || pri > d.pri[i]:
		d.bits[i], d.col[i], d.pri[i] = bit, c, pri
	case pri == d.pri[i]:
		d.bits[i] |= bit
		d.col[i] = c
	}
}
func (d *Dots) Line(x0, y0, x1, y1 float64, c Color, pri int8, dash int) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
	for i := 0; i <= n; i++ {
		if dash > 0 && (i/dash)%2 == 1 {
			continue
		}
		t := float64(i) / float64(n)
		d.Set(int(math.Round(x0+(x1-x0)*t)), int(math.Round(y0+(y1-y0)*t)), c, pri)
	}
}

// Curve samples a cubic Bézier and returns its points so callers can animate
// a pulse along the same trajectory.
func curve(p0, p1, p2, p3 [2]float64) [][2]float64 {
	length := math.Hypot(p3[0]-p0[0], p3[1]-p0[1]) + math.Hypot(p1[0]-p0[0], p1[1]-p0[1]) + math.Hypot(p3[0]-p2[0], p3[1]-p2[1])
	n := max(2, int(length*1.2))
	out := make([][2]float64, 0, n+1)
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		a, b, c, e := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
		out = append(out, [2]float64{a*p0[0] + b*p1[0] + c*p2[0] + e*p3[0], a*p0[1] + b*p1[1] + c*p2[1] + e*p3[1]})
	}
	return out
}
func (d *Dots) Polyline(pts [][2]float64, c Color, pri int8, dash int) {
	for i, p := range pts {
		if dash > 0 && (i/dash)%2 == 1 {
			continue
		}
		d.Set(int(math.Round(p[0])), int(math.Round(p[1])), c, pri)
	}
}

// Blit renders lit cells onto the canvas; unlit cells keep their content.
// With bg 0 a lit cell keeps the background already painted beneath it.
func (d *Dots) Blit(cv *Canvas, ox, oy int, bg Color) {
	for y := 0; y < d.H; y++ {
		for x := 0; x < d.W; x++ {
			i := y*d.W + x
			if d.bits[i] == 0 || !cv.in(ox+x, oy+y) {
				continue
			}
			b := bg
			if b == 0 {
				b = cv.at(ox+x, oy+y).st.BG
			}
			cv.Set(ox+x, oy+y, rune(0x2800+int(d.bits[i])), Style{FG: d.col[i], BG: b})
		}
	}
}

// bayer8 is the classic ordered-dither threshold matrix, normalised to 0..1.
var bayer8 = func() (m [8][8]float64) {
	b := [8][8]int{
		{0, 32, 8, 40, 2, 34, 10, 42}, {48, 16, 56, 24, 50, 18, 58, 26},
		{12, 44, 4, 36, 14, 46, 6, 38}, {60, 28, 52, 20, 62, 30, 54, 22},
		{3, 35, 11, 43, 1, 33, 9, 41}, {51, 19, 59, 27, 49, 17, 57, 25},
		{15, 47, 7, 39, 13, 45, 5, 37}, {63, 31, 55, 23, 61, 29, 53, 21},
	}
	for y := range b {
		for x := range b[y] {
			m[y][x] = (float64(b[y][x]) + .5) / 64
		}
	}
	return
}()

func dither(px, py int, v float64) bool { return v > bayer8[py&7][px&7] }

// Natural Earth 1:110m land (public domain), 0.5° cells, see scripts/gen_landmask.py.
//
//go:embed land.bin
var landBits []byte

const landW, landH = 720, 360

func land(lat, lon float64) bool {
	lon = math.Mod(lon+180, 360)
	if lon < 0 {
		lon += 360
	}
	col := min(landW-1, int(lon*2))
	row := min(landH-1, max(0, int((90-lat)*2)))
	i := row*landW + col
	return i>>3 < len(landBits) && landBits[i>>3]&(0x80>>(i&7)) != 0
}

func hash32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
