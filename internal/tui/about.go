package tui

import (
	"math"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Topophany = τόπος (place) + -phany (φαίνειν, to show): the appearance of
// place. The About art is a one-bit dithered globe on which the places
// declared in the topology are the only lights.

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
// the art literally shows *their* places appearing. Nodes at one declared
// place become one light. Links are left to the maps: on the globe they
// would only tangle.
func (m *Model) aboutPlaces() []place {
	nodes, _, _ := m.graph()
	ps := []place{}
	byPlace := map[string]int{}
	for _, n := range nodes {
		if !n.Located || n.Inherited {
			continue
		}
		key := n.Place
		if key == "" {
			key = n.ID
		}
		if k, ok := byPlace[key]; ok {
			ps[k].health = worseSignal(ps[k].health, nodeSignal(n))
			continue
		}
		name := n.Place
		if name == "" {
			name = n.Name
		}
		byPlace[key] = len(ps)
		ps = append(ps, place{name, n.Lat, n.Lon, nodeSignal(n)})
	}
	if len(ps) >= 2 {
		return ps
	}
	return defaultPlaces
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

var (
	cLandDay  = Hex(0xB4F5E8)
	cLandDusk = Hex(0x2F9389)
	cLandDark = Hex(0x1A4448)
	cSeaDot   = Hex(0x183F57)
	cSeaLit   = Hex(0x4A93B5)
	cLimb     = Hex(0x235A77)
	cCity     = Hex(0xFCD34D)
)

func (g globe) toWorld(v vec) vec {
	ts, tc := math.Sin(-g.tilt), math.Cos(-g.tilt)
	y, z := v[1]*tc-v[2]*ts, v[1]*ts+v[2]*tc
	s, c := math.Sin(g.spin), math.Cos(g.spin)
	return vec{v[0]*c + z*s, y, -v[0]*s + z*c}
}

// drawGlobe renders the globe centred in w×h cells and names the places
// facing the viewer. t is seconds since the About view opened; spin is extra
// rotation from the mouse wheel.
func (m *Model) drawGlobe(cv *Canvas, x, y, w, h int, t, spin float64) {
	d := NewDots(w, h)
	marks := m.paintGlobe(d, w*2, h*4, t, spin)
	d.Blit(cv, x, y, 0)
	// Names of the places facing the viewer, where they do not collide.
	used := map[[2]int]bool{}
	for _, mk := range marks {
		lx, ly := mk.x/2+2, mk.y/4
		n := width(mk.name)
		free := lx+n < w && ly >= 0 && ly < h
		for i := -1; i <= n && free; i++ {
			free = !used[[2]int{lx + i, ly}]
		}
		if !free {
			continue
		}
		for i := -1; i <= n; i++ {
			used[[2]int{lx + i, ly}] = true
		}
		cv.Text(x+lx, y+ly, mk.name, fg(Mix(mk.c, cText, .55)).bold(), -1)
	}
}

// globeMark is a place worth naming, in dots.
type globeMark struct {
	x, y int
	name string
	c    Color
}

// paintGlobe draws the globe centred on a W×H dot grid as one-bit dot art:
// land is an ordered dither lit by the sun with a crisp dotted coast, the
// ocean a faint halftone that follows the light, the limb a thin line with a
// haze beyond it. It returns the places facing the viewer.
func (m *Model) paintGlobe(d plotter, W, H int, t, spin float64) []globeMark {
	R := math.Min(float64(W), float64(H))/2 - math.Max(3, math.Min(float64(W), float64(H))*.05)
	if R < 6 {
		return nil
	}
	cx, cy := float64(W)/2, float64(H)/2
	places := m.aboutPlaces()
	// Start with the first declared place facing the viewer, then drift east.
	g := globe{spin: places[0].lon*math.Pi/180 - .35 + t*.11 + spin, tilt: .36}
	light := vec{-.62, .42, .66}.norm()
	half := vec{light[0], light[1], light[2] + 1}.norm()
	normal := func(px, py int) (vec, bool) {
		nx, ny := (float64(px)+.5-cx)/R, (float64(py)+.5-cy)/R
		r2 := nx*nx + ny*ny
		return vec{nx, -ny, math.Sqrt(math.Max(0, 1-r2))}, r2 <= 1
	}
	// Which dots are land, once per frame; the coast test reads neighbours.
	grid := make([]bool, W*H)
	for py := 0; py < H; py++ {
		for px := 0; px < W; px++ {
			if v, in := normal(px, py); in {
				wv := g.toWorld(v)
				grid[py*W+px] = land(math.Asin(clamp(wv[1], -1, 1))*180/math.Pi, math.Atan2(wv[0], wv[2])*180/math.Pi)
			}
		}
	}
	isLand := func(px, py int) bool { return px >= 0 && py >= 0 && px < W && py < H && grid[py*W+px] }
	// Shades are quantized: neighbouring cells share colors, which keeps
	// the escape stream small without visible banding at dot scale.
	landColor := func(lit, z float64) Color {
		lit, z = math.Round(lit*10)/10, math.Round(z*5)/5
		c := Mix(cLandDark, cLandDusk, math.Min(1, lit*3))
		if lit > .33 {
			c = Mix(cLandDusk, cLandDay, math.Pow((lit-.33)/.67, .8))
		}
		return Mix(cInk, c, .5+.5*math.Sqrt(z))
	}
	thick := math.Max(3, R*.09)
	for py := 0; py < H; py++ {
		for px := 0; px < W; px++ {
			dx, dy := float64(px)+.5-cx, float64(py)+.5-cy
			r := math.Hypot(dx, dy)
			switch {
			case r > R+thick*2:
				// Sparse stars far from the globe.
				hs := hash32(string([]byte{byte(px), byte(px >> 8), byte(py), byte(py >> 8)}))
				if hs%241 == 0 {
					tw := .5 + .5*math.Sin(t*(.9+float64(hs%7)*.3)+float64(hs%97))
					if tw > .3 {
						d.Set(px, py, Mix(cInk, cMuted, .25+.45*tw), 0)
					}
				}
			case r > R+thick:
			case r > R:
				// Atmosphere: an ordered-dither haze, brightest sunward.
				f := 1 - (r-R)/thick
				side := math.Max(0, (dx*light[0]-dy*light[1])/r)
				if dither(px, py, f*f*(.18+.82*side)) {
					d.Set(px, py, Mix(cInk, cAccent2, .25+.5*math.Round(f*4)/4), 1)
				}
			case r > R-1:
				// The limb: one thin line, so the sphere keeps its shape on
				// the night side too.
				d.Set(px, py, cLimb, 1)
			default:
				v, _ := normal(px, py)
				lit := math.Max(0, v.dot(light))
				if grid[py*W+px] {
					if !isLand(px-1, py) || !isLand(px+1, py) || !isLand(px, py-1) || !isLand(px, py+1) {
						d.Set(px, py, landColor(math.Max(lit, .2), v[2]), 2)
					} else if dither(px, py, .3+.7*lit) {
						d.Set(px, py, landColor(lit, v[2]), 2)
					}
					continue
				}
				// Ocean: a sparse halftone that follows the light, with a
				// soft specular glint.
				spec := math.Pow(math.Max(0, v.dot(half)), 26)
				if dither(px, py, .16*math.Pow(lit, 1.3)+.4*spec) {
					d.Set(px, py, Mix(cSeaDot, cSeaLit, math.Round(math.Min(1, lit*.7+spec)*6)/6), 0)
				}
			}
		}
	}
	project := func(v vec) (float64, float64, vec) {
		p := g.toView(v)
		return cx + p[0]*R, cy - p[1]*R, p
	}
	// Places: a bright core and a slow ring; their health colors them when
	// the topology has evidence, otherwise they are warm lights.
	marks := []globeMark{}
	for i, p := range places {
		sx, sy, v := project(sphere(p.lat, p.lon))
		if v[2] < .08 {
			continue
		}
		c := cCity
		if p.health != "" && p.health != "unknown" {
			c = signalColor(p.health)
		}
		core := 1
		if R < 40 {
			core = 0
		}
		for dy := -core; dy <= 1; dy++ {
			for dx := -core; dx <= 0; dx++ {
				d.Set(int(sx)+dx, int(sy)+dy, c, 9)
			}
		}
		grow := math.Min(1, R/60)
		ring := 2 + 3*grow*math.Mod(t*.6+float64(i)*.23, 1)
		halo(d, sx, sy, ring, Mix(c, cInk, .3+ring/8), 7)
		if v[2] > .3 && R >= 30 {
			marks = append(marks, globeMark{int(sx), int(sy), p.name, c})
		}
	}
	return marks
}

// drawWordmark sets TOPOPHANY in a 5x7 pixel face, two pixels per cell with
// half blocks, filled with an ordered-dither gradient that a slow highlight
// sweeps across. It dithers into view from the left on opening. Returns its
// height in rows.
func (m *Model) drawWordmark(cv *Canvas, x, y int, t float64) int {
	const word = "TOPOPHANY"
	gw, gh := len(word)*6-1, 7
	on := func(gx, gy int) bool {
		if gx < 0 || gy < 0 || gy >= gh || gx >= gw || gx%6 == 5 {
			return false
		}
		return font5x7[rune(word[gx/6])][gy][gx%6] == '#'
	}
	ramp := []Color{cGlow, Hex(0xA7F3E4), cAccent, Hex(0x2DB3A3), Hex(0x1F7F78)}
	band := math.Mod(t*9, float64(gw)*2.2) - 12
	pixel := func(gx, gy int) Color {
		reveal := clamp(t*1.4-float64(gx)/float64(gw)*.9, 0, 1)
		if on(gx, gy) {
			if reveal < bayer8[gy&7][gx&7] {
				return 0
			}
			v := float64(gy)/float64(gh-1)*float64(len(ramp)-1) + bayer8[gy&7][gx&7] - .5
			c := ramp[int(clamp(math.Round(v), 0, float64(len(ramp)-1)))]
			if b := math.Exp(-math.Pow(float64(gx)-band-float64(gy)*.6, 2) / 18); b > .05 {
				c = Mix(c, cGlow, b*.8)
			}
			return c
		}
		if on(gx-1, gy-1) && reveal >= 1 && !m.NoColor {
			return Mix(cAccent, cInk, .8) // the shadow
		}
		return 0
	}
	rows := (gh + 2) / 2
	for r := 0; r < rows; r++ {
		for gx := 0; gx <= gw; gx++ {
			top, bottom := pixel(gx, r*2), pixel(gx, r*2+1)
			switch {
			case top != 0 && bottom != 0 && top == bottom:
				cv.Set(x+gx, y+r, '█', fg(top))
			case top != 0 && bottom != 0:
				cv.Set(x+gx, y+r, '▀', fg(top).Bg(bottom))
			case top != 0:
				cv.Set(x+gx, y+r, '▀', fg(top))
			case bottom != 0:
				cv.Set(x+gx, y+r, '▄', fg(bottom))
			}
		}
	}
	return rows
}

type aboutLine struct {
	parts []span
}

// aboutText is the right-hand (or lower) column of the About view; the
// caption about the globe is left out when there is no globe.
func (m *Model) aboutText(globe bool) []aboutLine {
	c := m.Snapshot.Topology
	sp := func(s string, st Style) span { return span{s, st} }
	greek, latin, gloss := fg(cAccent).bold(), fg(cMuted).italic(), fg(cText)
	lines := []aboutLine{
		{[]span{sp("τόπος", greek), sp("   tópos    ", latin), sp("place", gloss)}},
		{[]span{sp("φαίνω", greek), sp("   phaínō   ", latin), sp("to bring to light · to appear", gloss)}},
		{},
		{[]span{sp("topophany", fg(cGlow).bold()), sp("  n.  ", latin), sp("the appearance of a place —", gloss)}},
		{[]span{sp("the moment a network shows where it actually is.", gloss)}},
		{},
		{[]span{sp("From where, through which path, reaching what,", fg(cAccent))}},
		{[]span{sp("and on what evidence.", fg(cAccent))}},
		{},
		{[]span{sp("Topophany "+model.Version, fg(cMuted)), sp("  ·  ", fg(cFaint)), sp("MIT", fg(cMuted)), sp("  ·  ", fg(cFaint)), sp("self-hosted", fg(cMuted)), sp("  ·  ", fg(cFaint)), sp("`topo`", fg(cMuted))}},
	}
	if c.Project != "" {
		lines = append(lines, aboutLine{[]span{sp(c.Project, fg(cText)), sp("  ·  "+itoa(len(c.Nodes))+" nodes  ·  "+itoa(len(c.Links))+" links  ·  "+itoa(len(c.Checks))+" checks", fg(cMuted))}})
	}
	if globe {
		lines = append(lines, aboutLine{}, aboutLine{[]span{sp("Each lit point on the globe is a place you declared.", fg(cFaint).italic())}})
	}
	return lines
}

const aboutTextW = 58

// drawAbout composes the globe and the text for the space available:
// side by side and centred when wide, the globe over the text when tall,
// the text alone when small.
func (m *Model) drawAbout(cv *Canvas, x, y, w, h int) {
	t := m.Now.Sub(m.aboutAt).Seconds()
	if m.aboutAt.IsZero() || t < 0 {
		t = 0
	}
	spin := m.aboutSpin
	lines := m.aboutText(true)
	textH := len(lines) + 5 // wordmark and a blank row
	big := w >= aboutTextW+4
	if !big {
		textH = len(lines) + 2
	}
	tw := min(aboutTextW, w-2)
	var gx, gy, gw, gh, tx, ty int
	switch {
	case w >= aboutTextW+44 && h >= 14:
		// Side by side: the globe is as tall as the view allows (and no
		// wider than leaves room for the text), the pair centred.
		gh = h
		gw = min(gh*2+2, w-aboutTextW-6)
		gh = min(gh, gw/2+1)
		total := gw + 6 + tw
		gx = x + max(0, (w-total)/2)
		gy = y + (h-gh)/2
		tx = gx + gw + 6
		ty = y + max(0, (h-textH)/2)
	case h >= textH+12 && w >= 30:
		// Stacked: the globe above, the text below, both centred.
		gh = min(h-textH-1, w/2)
		gw = min(w, gh*2+2)
		gx = x + (w-gw)/2
		gy = y + max(0, (h-gh-textH-1)/2)
		tx = x + max(1, (w-tw)/2)
		ty = gy + gh + 1
	default:
		tx = x + max(1, (w-tw)/2)
		ty = y + max(0, (h-textH)/2)
	}
	if gw >= 16 && gh >= 7 {
		m.drawGlobe(cv, gx, gy, gw, gh, t, spin)
	} else {
		lines = m.aboutText(false)
	}
	if big && ty+5 < y+h {
		ty += m.drawWordmark(cv, tx, ty, t) + 1
	} else {
		cv.Text(tx, ty, "T O P O P H A N Y", fg(cAccent).bold(), tw)
		ty += 2
	}
	// Lines fade up one after another on opening.
	for i, l := range lines {
		if ty >= y+h {
			break
		}
		k := clamp(t*1.6-float64(i)*.16, 0, 1)
		cx := tx
		for _, s := range l.parts {
			st := s.st
			st.FG = Mix(cInk, st.FG, k)
			cx += cv.Text(cx, ty, s.s, st, tx+tw-cx)
		}
		ty++
	}
}
