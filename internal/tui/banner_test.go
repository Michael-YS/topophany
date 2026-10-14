package tui

import (
	"image"
	"image/png"
	"math"
	"os"
	"testing"
)

// The README banner (docs/assets/banner.png) is drawn from the About art:
// the same shaded globe and the same 5x7 face, every dot a round dot as in
// a terminal, but each with its own color and the ground shaded per dot
// rather than per cell. `make banner` regenerates it; otherwise the test
// only checks that the art renders.

const (
	bannerW, bannerH = 300, 96 // dots
	bannerPitch      = 6       // pixels per dot
)

// dotImage is a per-dot raster for the paintGlobe plotter, with a ground
// that is shaded one dot at a time.
type dotImage struct {
	W, H   int
	c      []Color
	pri    []int8
	ground []Color
	cover  []float64
}

func newDotImage(w, h int) *dotImage {
	return &dotImage{W: w, H: h, c: make([]Color, w*h), pri: make([]int8, w*h), ground: make([]Color, w*h), cover: make([]float64, w*h)}
}

func (d *dotImage) Set(px, py int, c Color, pri int8) {
	if px < 0 || py < 0 || px >= d.W || py >= d.H {
		return
	}
	if i := py*d.W + px; d.c[i] == 0 || pri >= d.pri[i] {
		d.c[i], d.pri[i] = c, pri
	}
}

func (d *dotImage) Shade(px, py int, c Color, a float64) {
	if px >= 0 && py >= 0 && px < d.W && py < d.H && a > 0 {
		d.ground[py*d.W+px], d.cover[py*d.W+px] = c, a
	}
}

// shifted moves a plotter's origin.
type shifted struct {
	plotter
	x, y int
}

func (s shifted) Set(px, py int, c Color, pri int8) { s.plotter.Set(px+s.x, py+s.y, c, pri) }
func (s shifted) Shade(px, py int, c Color, a float64) {
	if sh, ok := s.plotter.(shader); ok {
		sh.Shade(px+s.x, py+s.y, c, a)
	}
}

// image draws every lit dot as an anti-aliased disc on the ink background,
// over the shaded ground.
func (d *dotImage) image(pitch int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, d.W*pitch, d.H*pitch))
	for py := 0; py < d.H; py++ {
		for px := 0; px < d.W; px++ {
			i := py*d.W + px
			gr, gg, gb := Mix(cInk, d.ground[i], d.cover[i]).rgb()
			if d.cover[i] == 0 {
				gr, gg, gb = cInk.rgb()
			}
			for y := py * pitch; y < (py+1)*pitch; y++ {
				for x := px * pitch; x < (px+1)*pitch; x++ {
					o := img.PixOffset(x, y)
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(gr), uint8(gg), uint8(gb), 255
				}
			}
		}
	}
	r := float64(pitch) * .36
	for py := 0; py < d.H; py++ {
		for px := 0; px < d.W; px++ {
			c := d.c[py*d.W+px]
			if c == 0 {
				continue
			}
			cr, cg, cb := c.rgb()
			cx, cy := (float64(px)+.5)*float64(pitch), (float64(py)+.5)*float64(pitch)
			for y := py * pitch; y < (py+1)*pitch; y++ {
				for x := px * pitch; x < (px+1)*pitch; x++ {
					a := clamp(r+.5-math.Hypot(float64(x)+.5-cx, float64(y)+.5-cy), 0, 1)
					if a == 0 {
						continue
					}
					o := img.PixOffset(x, y)
					img.Pix[o] = uint8(float64(img.Pix[o])*(1-a) + float64(cr)*a)
					img.Pix[o+1] = uint8(float64(img.Pix[o+1])*(1-a) + float64(cg)*a)
					img.Pix[o+2] = uint8(float64(img.Pix[o+2])*(1-a) + float64(cb)*a)
				}
			}
		}
	}
	return img
}

func banner() *image.RGBA {
	d := newDotImage(bannerW, bannerH)
	// No topology: the reference landmarks light the globe.
	m := &Model{}
	const gw = 104
	gx := bannerW - gw - 8
	m.paintGlobe(shifted{d, gx, 0}, gw, bannerH, 1, 7.5, 0)

	// TOPOPHANY, three dots to a pixel of the 5x7 face, filled with an
	// ordered-dither gradient and cast with a sparse shadow; under it the
	// gloss, one dot to a pixel, tracked out to the same width.
	const word, gloss, s, gap = "TOPOPHANY", "THE APPEARANCE OF A PLACE", 3, 7
	ww, wh := (len(word)*6-1)*s, 7*s
	wx, wy := (gx-ww)/2, (bannerH-wh-s-gap-7)/2
	on := func(x, y int) bool {
		fx, fy := x/s, y/s
		if x < 0 || y < 0 || fx >= len(word)*6 || fy >= 7 || fx%6 == 5 {
			return false
		}
		return font5x7[rune(word[fx/6])][fy][fx%6] == '#'
	}
	ramp := []Color{cGlow, Hex(0xA7F3E4), cAccent, Hex(0x2DB3A3), Hex(0x1F7F78)}
	for y := 0; y < wh+s; y++ {
		for x := 0; x < ww+s; x++ {
			switch {
			case on(x, y):
				v := float64(y)/float64(wh-1)*float64(len(ramp)-1) + bayer8[y&7][x&7] - .5
				c := ramp[int(clamp(math.Round(v), 0, float64(len(ramp)-1)))]
				// A glint crossing the P and H.
				if g := math.Exp(-math.Pow(float64(x)-float64(ww)*.52-float64(y)*.6, 2) / 40); g > .05 {
					c = Mix(c, cGlow, g*.8)
				}
				d.Set(wx+x, wy+y, c, 9)
			case on(x-s/2-1, y-s/2-1) && dither(x, y, .5):
				d.Set(wx+x, wy+y, Mix(cAccent, cInk, .7), 8)
			}
		}
	}
	face := map[rune][7]string{
		'E': {"#####", "#....", "#....", "####.", "#....", "#....", "#####"},
		'R': {"####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"},
		'C': {".###.", "#...#", "#....", "#....", "#....", "#...#", ".###."},
		'F': {"#####", "#....", "#....", "####.", "#....", "#....", "#...."},
		'L': {"#....", "#....", "#....", "#....", "#....", "#....", "#####"},
	}
	for r, g := range font5x7 {
		face[r] = g
	}
	advance := func(r rune) int {
		if r == ' ' {
			return 4
		}
		return 7
	}
	glossW := -2
	for _, r := range gloss {
		glossW += advance(r)
	}
	tx, ty := wx+(ww-glossW)/2, wy+wh+s+gap
	for _, r := range gloss {
		g, ok := face[r]
		for y := 0; y < 7 && ok; y++ {
			for x := 0; x < 5; x++ {
				if g[y][x] == '#' {
					d.Set(tx+x, ty+y, Mix(cAccent, cInk, .45), 9)
				}
			}
		}
		tx += advance(r)
	}

	// Sparse stars where nothing else is, none near the words.
	for y := 0; y < bannerH; y++ {
		for x := 0; x < gx; x++ {
			if x >= wx-6 && x < wx+ww+s+6 && y >= wy-4 && y < ty+7+4 {
				continue
			}
			if h := hash32(string([]byte{byte(x), byte(x >> 8), byte(y), 's'})); h%173 == 0 {
				d.Set(x, y, Mix(cInk, cMuted, .2+.5*float64(h>>8%100)/100), 0)
			}
		}
	}
	return d.image(bannerPitch)
}

func TestBanner(t *testing.T) {
	img := banner()
	if b := img.Bounds(); b.Dx() != bannerW*bannerPitch || b.Dy() != bannerH*bannerPitch {
		t.Fatalf("banner is %v", b)
	}
	lit := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+1] > 200 {
			lit++
		}
	}
	if lit < 1000 {
		t.Fatalf("banner has %d bright pixels", lit)
	}
	out := os.Getenv("TOPO_BANNER")
	if out == "" {
		return
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
