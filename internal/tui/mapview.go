//go:build !nodevice

package tui

import "strings"

// The map on a touch screen: a pinch magnifies it and a drag moves the
// window over it, as a map does on a phone. Magnified, the map is drawn on a
// larger canvas, where the virtual map's cards and the geo map's places
// get the room to say more, and the screen shows a window of it. Until a
// drag moves the window it follows the selection: the node, or the route,
// that the selected row lights up. The legend stays along the foot.

// mapIn draws the map where a layout puts it: on a device's screen as a
// touch screen shows it, otherwise as the terminal does.
func (m *Model) mapIn(cv *Canvas, x, y, w, h int, sel Row) {
	if m.Device != nil {
		m.mapView(cv, x, y, w, h, sel)
		return
	}
	m.drawMap(cv, x, y, w, h, sel)
}

// mapView draws the map into the w×h cells at (x, y) as a touch screen
// shows it, and remembers where, for pinches and drags.
func (m *Model) mapView(cv *Canvas, x, y, w, h int, sel Row) {
	m.touch.mapRect = rect{x, y, w, h}
	scale := mapScales[min(max(m.touch.zoom, 0), len(mapScales)-1)]
	if scale == 1 || w < 8 || h < 3 {
		m.drawMap(cv, x, y, w, h, sel)
		return
	}
	nodes, edges, groups := m.graph()
	f := m.focus(sel)
	legend := h >= 6
	mh := h
	if legend {
		mh--
	}
	W, H := int(float64(w)*scale+.5), int(float64(mh)*scale+.5)
	off := NewCanvas(W, H)
	off.Base = cv.Base
	off.Hyperlinks = cv.Hyperlinks
	first := len(m.hits)
	m.drawMapBody(off, 0, 0, W, H, nodes, edges, groups, f)
	px, py := m.touch.pan[0], m.touch.pan[1]
	if !m.touch.panned {
		// The window centres on what the selection lights up, where the
		// map drew it, else on the middle.
		px, py = (W-w)/2, (H-mh)/2
		x0, y0, x1, y1 := W, H, -1, -1
		for _, ht := range m.hits[first:] {
			id, ok := strings.CutPrefix(ht.key, "select:node:")
			if !ok || !f.hasNode(id) {
				continue
			}
			x0, y0 = min(x0, ht.x), min(y0, ht.y)
			x1, y1 = max(x1, ht.x+ht.w), max(y1, ht.y+ht.h)
		}
		if x1 >= 0 {
			// Centred where it fits; where it does not, from where the
			// route starts, so the window shows the start and runs on.
			px, py = (x0+x1-w)/2, (y0+y1-mh)/2
			if x1-x0 > w {
				px = x0 - 2
			}
			if y1-y0 > mh {
				py = y0 - 2
			}
		}
	}
	px, py = min(max(px, 0), W-w), min(max(py, 0), H-mh)
	m.touch.pan = [2]int{px, py}
	cv.Blit(off, px, py, x, y, w, mh)
	// The map's targets move with it, and those outside the window go.
	kept := m.hits[:first]
	for _, ht := range m.hits[first:] {
		ht.x += x - px
		ht.y += y - py
		x0, x1 := max(ht.x, x), min(ht.x+ht.w, x+w)
		y0, y1 := max(ht.y, y), min(ht.y+ht.h, y+mh)
		if x1 <= x0 || y1 <= y0 {
			continue
		}
		ht.x, ht.w, ht.y, ht.h = x0, x1-x0, y0, y1-y0
		kept = append(kept, ht)
	}
	m.hits = kept
	if legend {
		m.mapLegend(cv, x, y+h-1, w, nodes, edges, groups)
	}
	// Where the window sits on the whole map, along its right and foot.
	if H > mh {
		scrollThumb(cv, x+w, y, mh, H, py, fg(Mix(cLine, cAccent, .45)))
	}
}

// zoomLabel says how far the map is magnified.
func (m *Model) zoomLabel() string {
	switch s := mapScales[m.touch.zoom]; s {
	case 1:
		return "1×"
	case 1.5:
		return "1.5×"
	default:
		return itoa(int(s)) + "×"
	}
}

// mapControls are the map's touch controls, three rows tall at (x, y):
// out, in and the map alone. It returns their width.
func (m *Model) mapControls(cv *Canvas, x, y int) int {
	full := "⤢"
	if m.FullMap {
		full = "⤡"
	}
	return m.segmented(cv, x, y, []segment{{"−", "touch:zoom-out"}, {"+", "touch:zoom-in"}, {full, "key:f"}}, -1)
}

// mapModes are the map's views as a segmented control offers them.
func (m *Model) mapSegments() ([]segment, int) {
	segs := []segment{{"Virtual", "touch:map:virtual"}, {"Geo", "touch:map:geo"}, {"Network", "touch:map:network"}}
	return segs, indexOf(mapModes, m.mapMode())
}

// mapPanel is the map in a panel; with controls, a row of them along its
// top, the map's views on the left and its zoom on the right, where they
// never cover what the map draws. The panel's tag says the view and the
// magnification.
func (m *Model) mapPanel(cv *Canvas, r rect, title string, sel Row, controls bool) {
	tag := "· " + mapNames[m.mapMode()] + " · " + m.zoomLabel()
	ix, iy, iw, ih := m.panel(cv, r.x, r.y, r.w, r.h, title, tag, false)
	if segs, at := m.mapSegments(); controls && ih >= 12 && iw >= segmentsWidth(segs)+14 {
		m.segmented(cv, ix, iy, segs, at)
		m.mapControls(cv, ix+iw-13, iy)
		iy, ih = iy+touchRows, ih-touchRows
	}
	m.mapView(cv, ix-1, iy, iw+2, ih, sel)
}

// segmentsWidth is how wide segmented draws segs.
func segmentsWidth(segs []segment) int {
	w := 1
	for _, s := range segs {
		w += width(s.label) + 3
	}
	return w
}

// pinch magnifies the map under (x, y) by steps, or shrinks it.
func (m *Model) pinch(steps int) {
	z := min(max(m.touch.zoom+steps, 0), len(mapScales)-1)
	if z != m.touch.zoom && m.touch.panned {
		// The window keeps its middle where it was on the map.
		r, a, b := m.touch.mapRect, mapScales[m.touch.zoom], mapScales[z]
		cx, cy := float64(m.touch.pan[0])+float64(r.w)/2, float64(m.touch.pan[1])+float64(r.h)/2
		m.touch.pan = [2]int{int(cx*b/a) - r.w/2, int(cy*b/a) - r.h/2}
	}
	m.touch.zoom = z
	if z == 0 {
		m.touch.panned = false
	}
}

// drag moves the magnified map's window by dx, dy cells, against the
// finger, as a map follows it.
func (m *Model) drag(dx, dy int) {
	if m.touch.zoom == 0 {
		return
	}
	m.touch.pan = [2]int{m.touch.pan[0] - dx, m.touch.pan[1] - dy}
	m.touch.panned = true
}
