package tui

import (
	"math"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Topophany = τόπος (place) + -phany (φαίνειν, to show): the appearance of
// place. The About art is a globe whose declared places surface as light and
// connect, rendered as an ordered-dither braille dot matrix.

var font5x7 = map[rune][7]string{
	'T': {"#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."},
	'O': {".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'P': {"####.", "#...#", "#...#", "####.", "#....", "#....", "#...."},
	'H': {"#...#", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'A': {".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'N': {"#...#", "##..#", "#.#.#", "#..##", "#...#", "#...#", "#...#"},
	'Y': {"#...#", "#...#", ".#.#.", "..#..", "..#..", "..#..", "..#.."},
}

type place struct {
	name     string
	lat, lon float64
	health   string
}

var defaultPlaces = []place{
	{"Shanghai", 31.2, 121.5, ""}, {"Tokyo", 35.7, 139.7, ""}, {"Singapore", 1.35, 103.8, ""},
	{"Frankfurt", 50.1, 8.7, ""}, {"London", 51.5, -0.1, ""}, {"Ashburn", 39.0, -77.5, ""},
	{"Los Angeles", 34.05, -118.25, ""}, {"São Paulo", -23.5, -46.6, ""}, {"Sydney", -33.9, 151.2, ""},
	{"Mumbai", 19.1, 72.9, ""},
}

// aboutPlaces uses the operator's declared node locations when present, so
// the art literally shows *their* places appearing.
func (m *Model) aboutPlaces() ([]place, [][2]int) {
	nodes, edges, _ := m.graph()
	ps := []place{}
	at := map[string]int{}
	for _, n := range nodes {
		if !n.Located || n.Inherited {
			continue
		}
		at[n.ID] = len(ps)
		ps = append(ps, place{n.Name, n.Lat, n.Lon, n.Health})
	}
	arcs := [][2]int{}
	if len(ps) >= 2 {
		seen := map[[2]int]bool{}
		for _, e := range edges {
			a, okA := at[e.From]
			b, okB := at[e.To]
			if okA && okB && a != b && !seen[[2]int{a, b}] {
				seen[[2]int{a, b}] = true
				arcs = append(arcs, [2]int{a, b})
			}
		}
		return ps, arcs
	}
	for i := range defaultPlaces {
		arcs = append(arcs, [2]int{i, (i + 3) % len(defaultPlaces)})
	}
	return defaultPlaces, arcs
}

type vec [3]float64

func (a vec) dot(b vec) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a vec) norm() vec {
	l := math.Sqrt(a.dot(a))
	return vec{a[0] / l, a[1] / l, a[2] / l}
}
func sphere(lat, lon float64) vec {
	la, lo := lat*math.Pi/180, lon*math.Pi/180
	return vec{math.Cos(la) * math.Sin(lo), math.Sin(la), math.Cos(la) * math.Cos(lo)}
}

// globe transforms: world -> view rotates by spin around the polar axis and
// then tilts the north pole toward the viewer.
type globe struct{ spin, tilt float64 }

func (g globe) toView(v vec) vec {
	s, c := math.Sin(-g.spin), math.Cos(-g.spin)
	x, z := v[0]*c+v[2]*s, -v[0]*s+v[2]*c
	y := v[1]
	ts, tc := math.Sin(g.tilt), math.Cos(g.tilt)
	return vec{x, y*tc - z*ts, y*ts + z*tc}
}
func (g globe) toWorld(v vec) vec {
	ts, tc := math.Sin(-g.tilt), math.Cos(-g.tilt)
	y, z := v[1]*tc-v[2]*ts, v[1]*ts+v[2]*tc
	s, c := math.Sin(g.spin), math.Cos(g.spin)
	return vec{v[0]*c + z*s, y, -v[0]*s + z*c}
}

func (m *Model) drawGlobe(cv *Canvas, x, y, w, h int, t float64) {
	d := NewDots(w, h)
	W, H := float64(w*2), float64(h*4)
	R := math.Min(W, H)/2 - 3
	if R < 4 {
		return
	}
	cx, cy := W/2, H/2
	places, arcs := m.aboutPlaces()
	// Start with the first declared place facing the viewer, then drift east.
	g := globe{spin: places[0].lon*math.Pi/180 - .5 + t*.16, tilt: .38}
	light := vec{-.55, .45, .70}.norm()
	half := vec{light[0], light[1], light[2] + 1}.norm()
	landAt := func(px, py int) bool {
		nx, ny := (float64(px)+.5-cx)/R, (float64(py)+.5-cy)/R
		r2 := nx*nx + ny*ny
		if r2 > 1 {
			return false
		}
		wv := g.toWorld(vec{nx, -ny, math.Sqrt(1 - r2)})
		return land(math.Asin(clamp(wv[1], -1, 1))*180/math.Pi, math.Atan2(wv[0], wv[2])*180/math.Pi)
	}
	for py := 0; py < h*4; py++ {
		for px := 0; px < w*2; px++ {
			nx, ny := (float64(px)+.5-cx)/R, (float64(py)+.5-cy)/R
			r2 := nx*nx + ny*ny
			if r2 > 1 {
				if r2 < 1.16 {
					glow := (1.16 - r2) / .16
					if dither(px, py, glow*glow*.55) {
						d.Set(px, py, Mix(cDeep, cAccent2, glow*.8), 0)
					}
					continue
				}
				// Sparse twinkling starfield.
				hs := hash32(string([]byte{byte(px), byte(px >> 8), byte(py), byte(py >> 8)}))
				if hs%173 == 0 {
					tw := .5 + .5*math.Sin(t*(1.3+float64(hs%7)*.4)+float64(hs%97))
					if tw > .35 {
						d.Set(px, py, Mix(cFaint, cGlow, tw*.7), 0)
					}
				}
				continue
			}
			v := vec{nx, -ny, math.Sqrt(1 - r2)}
			wv := g.toWorld(v)
			lat := math.Asin(clamp(wv[1], -1, 1)) * 180 / math.Pi
			lon := math.Atan2(wv[0], wv[2]) * 180 / math.Pi
			diff := math.Max(0, v.dot(light))
			if land(lat, lon) {
				if !landAt(px-1, py) || !landAt(px+1, py) || !landAt(px, py-1) || !landAt(px, py+1) {
					d.Set(px, py, Mix(cCoast, cGlow, math.Pow(diff, 1.3)*.8), 1)
					continue
				}
				b := .06 + .62*diff
				if diff < .06 {
					// Night side: scattered "city lights" are places appearing in the dark.
					cell := hash32(string([]byte{byte(int(lat*1.5) + 128), byte(int(lon*1.5) + 128), byte(int(lon*1.5)>>8 + 9)}))
					if cell%19 == 0 && dither(px, py, .7) {
						d.Set(px, py, Hex(0xFCD34D), 2)
						continue
					}
				}
				if dither(px, py, b) {
					d.Set(px, py, Mix(cLand, cGlow, math.Pow(diff, 1.6)*.75), 1)
				}
				continue
			}
			spec := math.Pow(math.Max(0, v.dot(half)), 28)
			b := .02 + .12*diff*diff + .8*spec
			if math.Abs(math.Mod(lat+900, 15)) < .7 || math.Abs(math.Mod(lon+1800, 15)) < .7 {
				b += .1 + .1*diff
			}
			if dither(px, py, b) {
				d.Set(px, py, Mix(cOcean, cAccent2, math.Min(1, diff*.5+spec)), 0)
			}
		}
	}
	project := func(v vec) (float64, float64, bool) {
		p := g.toView(v)
		sx, sy := cx+p[0]*R, cy-p[1]*R
		visible := p[2] > 0 || p[0]*p[0]+p[1]*p[1] > 1
		return sx, sy, visible
	}
	// Great-circle arcs lifted off the surface, with a travelling pulse.
	for k, a := range arcs {
		va, vb := sphere(places[a[0]].lat, places[a[0]].lon), sphere(places[a[1]].lat, places[a[1]].lon)
		omega := math.Acos(clamp(va.dot(vb), -1, 1))
		if omega < 1e-3 {
			continue
		}
		steps := int(omega*R*1.6) + 8
		head := math.Mod(t*.35+float64(k)*.37, 1.4)
		for i := 0; i <= steps; i++ {
			s := float64(i) / float64(steps)
			sa, sb := math.Sin((1-s)*omega)/math.Sin(omega), math.Sin(s*omega)/math.Sin(omega)
			lift := 1 + .18*math.Sin(math.Pi*s)*math.Min(1, omega)
			p := vec{(va[0]*sa + vb[0]*sb) * lift, (va[1]*sa + vb[1]*sb) * lift, (va[2]*sa + vb[2]*sb) * lift}
			sx, sy, ok := project(p)
			if !ok {
				continue
			}
			dist := head - s
			switch {
			case dist >= 0 && dist < .06:
				d.Set(int(sx), int(sy), Mix(cGlow, cAccent, dist/.06), 8)
			case i%2 == 0:
				d.Set(int(sx), int(sy), Mix(cAccent, cDeep, .35), 5)
			}
		}
	}
	// Places: a bright core and a breathing ring.
	for i, p := range places {
		sx, sy, ok := project(sphere(p.lat, p.lon))
		if !ok || sphere(p.lat, p.lon) == (vec{}) {
			continue
		}
		if g.toView(sphere(p.lat, p.lon))[2] < .05 {
			continue
		}
		c := cGlow
		if p.health != "" && p.health != "unknown" {
			c = healthColor(p.health)
		}
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				d.Set(int(sx)+dx, int(sy)+dy, c, 9)
			}
		}
		ring := 2.5 + 2.5*math.Mod(t*.8+float64(i)*.23, 1)
		for a := 0.0; a < 2*math.Pi; a += .35 {
			d.Set(int(sx+math.Cos(a)*ring), int(sy+math.Sin(a)*ring*.9), Mix(c, cDeep, ring/6), 7)
		}
	}
	d.Blit(cv, x, y, 0)
}

// drawWordmark renders TOPOPHANY from a 5x7 font where each font pixel is a
// 2x2 block of braille dots. Dot density follows a dithered left-to-right
// reveal, a travelling highlight, and a faint dithered halo around strokes.
func (m *Model) drawWordmark(cv *Canvas, x, y int, t float64) int {
	const word = "TOPOPHANY"
	gw, gh := len(word)*6-1, 7
	on := func(gx, gy int) bool {
		if gx < 0 || gy < 0 || gy >= gh || gx >= gw || gx%6 == 5 {
			return false
		}
		return font5x7[rune(word[gx/6])][gy][gx%6] == '#'
	}
	d := NewDots(gw, (gh*2+3)/4+1)
	for py := 0; py < d.H*4; py++ {
		for px := 0; px < d.W*2; px++ {
			gx, gy := px/2, py/2
			reveal := clamp((t*1.1-float64(gx)/float64(gw)*.9-bayer8[py&7][px&7]*.5)*2.2, 0, 1)
			band := math.Exp(-math.Pow(math.Mod(t*.55, 2.4)*36-float64(gx)+float64(gy)*.5-8, 2) / 20)
			if on(gx, gy) {
				v := reveal * (.9 + .1*band)
				if dither(px, py, v) {
					d.Set(px, py, Mix(Mix(cAccent, cAccent2, float64(gx)/float64(gw)), cGlow, band*.85), 2)
				}
				continue
			}
			near := 0
			for _, o := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-1, -1}, {1, 1}, {-1, 1}, {1, -1}} {
				if on(gx+o[0], gy+o[1]) {
					near++
				}
			}
			if near > 0 && dither(px, py, reveal*(.02+.025*float64(near))*(1+2*band)) {
				d.Set(px, py, Mix(cDeep, cAccent, .45+.4*band), 1)
			}
		}
	}
	d.Blit(cv, x, y, 0)
	return d.H
}

func (m *Model) drawAbout(cv *Canvas, x, y, w, h int) {
	t := m.Now.Sub(m.aboutAt).Seconds()
	if m.aboutAt.IsZero() || t < 0 {
		t = 0
	}
	textW := 58
	gw := min(w-textW-2, h*2+4)
	if w < 90 {
		gw = 0
	}
	if gw >= 16 {
		m.drawGlobe(cv, x, y, gw, h, t)
	} else {
		gw = 0
	}
	tx := x + gw + 2
	tw := x + w - tx
	if gw == 0 {
		tx, tw = x+2, w-4
	}
	ty := y + max(0, (h-24)/2)
	if gw == 0 && h >= 26 && w >= 30 {
		// Narrow terminals: the globe sits above the text.
		gh := h - 22
		m.drawGlobe(cv, x+max(0, (w-gh*2-4)/2), y, min(w, gh*2+4), gh, t)
		ty = y + gh
	}
	if tw >= 54 && ty+7 < y+h {
		ty += m.drawWordmark(cv, tx, ty, t) + 1
	} else {
		cv.Text(tx, ty, "T O P O P H A N Y", m.st(fg(cAccent).bold()), tw)
		ty += 2
	}
	fade := func(i int) Style {
		k := clamp(t*1.6-float64(i)*.18, 0, 1)
		return fg(Mix(cDeep, cText, k))
	}
	c := m.Snapshot.Topology
	lines := []struct {
		text string
		st   func(int) Style
	}{
		{"τόπος   tópos     place", func(i int) Style { return fade(i).bold() }},
		{"φαίνω   phaínō    to bring to light · to appear", func(i int) Style { return fade(i).bold() }},
		{"", fade},
		{"topophany  n.  the appearance of a place —", fade},
		{"the moment a network shows where it actually is.", fade},
		{"", fade},
		{"From where, through which path, reaching what,", func(i int) Style { return fg(Mix(cDeep, cAccent, clamp(t*1.6-float64(i)*.18, 0, 1))) }},
		{"and on what evidence.", func(i int) Style { return fg(Mix(cDeep, cAccent, clamp(t*1.6-float64(i)*.18, 0, 1))) }},
		{"", fade},
		{"Topophany " + model.Version + "  ·  MIT  ·  self-hosted  ·  `topo`", func(i int) Style { return fade(i).Fg(Mix(cDeep, cMuted, clamp(t*1.6-float64(i)*.18, 0, 1))) }},
		{"", fade},
	}
	if c.Project != "" {
		lines = append(lines, struct {
			text string
			st   func(int) Style
		}{c.Project + "  ·  " + itoa(len(c.Nodes)) + " nodes  ·  " + itoa(len(c.Links)) + " links  ·  " + itoa(len(c.Checks)) + " checks", func(i int) Style { return fg(Mix(cDeep, cMuted, clamp(t*1.6-float64(i)*.18, 0, 1))) }})
	}
	lines = append(lines, struct {
		text string
		st   func(int) Style
	}{"Each lit point on the globe is a place you declared.", func(i int) Style { return fg(Mix(cDeep, cFaint, clamp(t*1.6-float64(i)*.18, 0, 1))).italic() }})
	for i, l := range lines {
		if ty >= y+h {
			break
		}
		cv.Text(tx, ty, l.text, m.st(l.st(i)), tw)
		ty++
	}
}
