//go:build !nodevice

package tui

import (
	"strconv"
	"strings"
)

// The simulator (topo preview --device): the device's screen at its size
// in cells, in its bezel in the middle of the terminal, with a bar of the
// simulator's own controls under it. The mouse stands in for a finger: a
// click is a tap, a drag a swipe, the wheel over the map a pinch. [ and ]
// fold and unfold through the device's poses, \ picks the next device.

// What the simulator draws around the screen.
var (
	cDesk  = Hex(0x05080B) // the terminal around the device
	cBezel = Hex(0x1C2128) // the device's frame
	cGlass = Hex(0x000000) // the Dynamic Island, the cameras
)

// screen measures the device the TUI is on; a fitted one is the
// terminal's size.
func (m *Model) screen() display {
	d := *m.Device
	if d.Fit {
		d.Size = [2]float64{float64(m.Width) * cellW, float64(m.Height) * cellH}
	}
	return d.display()
}

// simulate draws the device's screen in the terminal.
func (m *Model) simulate() *Canvas {
	s := m.screen()
	if m.Device.Fit {
		// The screen is the terminal: no bezel and no simulator's bar.
		m.touch.origin = [2]int{}
		return m.drawScreen(s)
	}
	cv := NewCanvas(m.Width, m.Height)
	cv.Hyperlinks = m.Hyperlinks
	cv.Base = cDesk
	needW, needH := s.w+4, s.h+3
	if m.Width < needW || m.Height < needH {
		m.stage = nil
		lines := []span{
			{m.Device.Model + " · " + m.Device.Pose, fg(cText).bold()},
			{"needs " + itoa(needW) + "×" + itoa(needH) + " cells; this terminal has " + itoa(m.Width) + "×" + itoa(m.Height) + ".", fg(cMuted)},
			{"Enlarge the window or make its font smaller.", fg(cFaint)},
		}
		for i, l := range lines {
			cv.Text(max(0, (m.Width-width(l.s))/2), m.Height/2-2+i, l.s, l.st, m.Width)
		}
		m.simBar(cv, m.Height-1)
		return cv
	}
	ox, oy := (m.Width-s.w)/2, max(1, (m.Height-1-s.h)/2)
	m.touch.origin = [2]int{ox, oy}
	screenCv := m.drawScreen(s)
	// The bezel: a frame a cell wide around the glass, its corners round.
	frame := Style{FG: cBezel, BG: cDesk}
	cv.Fill(ox-1, oy, 1, s.h, Style{BG: cBezel})
	cv.Fill(ox+s.w, oy, 1, s.h, Style{BG: cBezel})
	for x := ox - 1; x <= ox+s.w; x++ {
		cv.Set(x, oy-1, '▄', frame)
		cv.Set(x, oy+s.h, '▀', frame)
	}
	cv.Set(ox-2, oy-1, '▗', frame)
	cv.Set(ox+s.w+1, oy-1, '▖', frame)
	cv.Set(ox-2, oy+s.h, '▝', frame)
	cv.Set(ox+s.w+1, oy+s.h, '▘', frame)
	for y := oy; y < oy+s.h; y++ {
		cv.Set(ox-2, y, '▐', frame)
		cv.Set(ox+s.w+1, y, '▌', frame)
	}
	cv.Blit(screenCv, 0, 0, ox, oy, s.w, s.h)
	for i := range m.hits {
		m.hits[i].x += ox
		m.hits[i].y += oy
	}
	// The hardware in the glass: the Dynamic Island, the cover's camera.
	for _, o := range s.occlusions {
		if o != s.clock {
			cv.Fill(ox+o.x, oy+o.y, o.w, o.h, Style{BG: cGlass})
		}
	}
	if !s.crease.empty() && s.CreaseActive {
		// Part folded, the crease catches the light differently: a shade
		// down the glass, the simulator's and not the TUI's.
		cv.Tint(ox+s.crease.x, oy+s.crease.y, s.crease.w, s.crease.h, func(st Style) Style {
			if st.BG == 0 {
				st.BG = cInk
			}
			if st.FG == 0 {
				st.FG = cText
			}
			st.BG, st.FG = Mix(st.BG, Hex(0), .45), Mix(st.FG, Hex(0), .3)
			return st
		})
	}
	m.simBar(cv, m.Height-1)
	return cv
}

// simBar is the simulator's own bar: the device's poses and the other
// devices, each a click away, and what the mouse does.
func (m *Model) simBar(cv *Canvas, y int) {
	cv.Fill(0, y, cv.W, 1, Style{BG: cInkHi})
	x := 1
	put := func(s string, st Style, act string) {
		n := cv.Text(x, y, s, st, cv.W-x-1)
		if act != "" {
			m.hit(x, y, n, act)
		}
		x += n
	}
	put("SIMULATOR ", fg(cEdit).Bg(cInkHi).bold(), "")
	for _, d := range m.Device.poses() {
		st := fg(cMuted).Bg(cInkHi)
		if d.ID == m.Device.ID {
			st = fg(cInk).Bg(cAccent).bold()
		}
		put(" "+d.Pose+" ", st, "sim:"+d.ID)
		x++
	}
	put("  [ ] fold   ", fg(cFaint).Bg(cInkHi), "")
	seen := map[string]bool{}
	for _, d := range devices {
		if seen[d.Model] {
			continue
		}
		seen[d.Model] = true
		st := fg(cMuted).Bg(cInkHi)
		if d.Model == m.Device.Model {
			st = fg(cGlow).Bg(cKeyBG).bold()
		}
		put(" "+d.Model+" ", st, "sim:"+d.ID)
		x++
	}
	put("  \\ device  ·  click tap · drag swipe · wheel on the map pinch", fg(cFaint).Bg(cInkHi), "")
}

// simKey handles the simulator's own keys and controls; ok is false for
// any other key.
func (m *Model) simKey(k string) (string, bool) {
	if m.Device.Fit {
		return "", false // no simulator: the screen is the terminal
	}
	if id, found := strings.CutPrefix(k, "sim:"); found {
		if d, e := DeviceByID(id); e == nil {
			m.setDevice(d)
		}
		return "", true
	}
	poses := m.Device.poses()
	at := 0
	for i, d := range poses {
		if d.ID == m.Device.ID {
			at = i
		}
	}
	switch k {
	case "]":
		m.setDevice(&poses[(at+1)%len(poses)])
	case "[":
		m.setDevice(&poses[(at+len(poses)-1)%len(poses)])
	case "\\":
		models := []string{}
		for _, d := range devices {
			if !contains(models, d.Model) {
				models = append(models, d.Model)
			}
		}
		next := models[(indexOf(models, m.Device.Model)+1)%len(models)]
		for i := range devices {
			if devices[i].Model == next {
				d := devices[i]
				m.setDevice(&d)
				break
			}
		}
	default:
		return "", false
	}
	return "", true
}

// setDevice puts the TUI on another device or into another pose. What the
// viewer was doing carries over: the tab, the selection, the evidence open.
func (m *Model) setDevice(d *Device) {
	m.Device = d
	m.touch.drawer = false
	m.touch.pressed = false
}

// gesture turns the simulator's mouse into a finger: a press and a release
// on one cell is a tap; apart, a swipe; the wheel over the map a pinch,
// elsewhere a scroll. It returns the key the gesture means, or "".
func (m *Model) gesture(b, x, y int, release bool) string {
	o := m.touch.origin
	sx, sy := x-o[0], y-o[1] // in the screen's cells
	mapAt := m.touch.mapRect.contains(sx, sy)
	switch b &^ (4 | 8 | 16) {
	case 64, 65:
		in := b&^(4|8|16) == 64
		if mapAt && m.modal == nil && !m.touch.drawer {
			m.pinch(map[bool]int{true: 1, false: -1}[in])
			return ""
		}
		if m.modal != nil {
			return map[bool]string{true: "wheel-up", false: "wheel-down"}[in]
		}
		if m.Detail || m.Tab == tabAbout || m.touch.layout == layoutRegular {
			return map[bool]string{true: "wheel-up", false: "wheel-down"}[in]
		}
		m.scrollList(map[bool]int{true: -1, false: 1}[in])
		return ""
	case 0:
		if !release {
			m.touch.down, m.touch.pressed = [2]int{x, y}, true
			return ""
		}
		if !m.touch.pressed {
			return ""
		}
		m.touch.pressed = false
		dx, dy := x-m.touch.down[0], y-m.touch.down[1]
		if abs(dx) <= 1 && abs(dy) <= 0 {
			return m.hitAt(m.touch.down[0], m.touch.down[1])
		}
		start := [2]int{m.touch.down[0] - o[0], m.touch.down[1] - o[1]}
		return m.swipe(start, dx, dy)
	}
	return ""
}

// swipe is a finger moved dx, dy cells from start, in the screen's cells.
func (m *Model) swipe(start [2]int, dx, dy int) string {
	if m.modal != nil {
		return ""
	}
	if m.touch.mapRect.contains(start[0], start[1]) && m.touch.zoom > 0 {
		m.drag(dx, dy)
		return ""
	}
	s := m.screen()
	if abs(dx) > 2*abs(dy) {
		// Across: from the left edge the drawer comes out; elsewhere on a
		// phone's page, the next or the previous tab.
		if dx > 0 && start[0] <= s.page.x+1 && (m.touch.layout == layoutCompact || m.touch.layout == layoutWide) {
			m.touch.drawer = true
			return ""
		}
		if m.touch.drawer && dx < 0 {
			m.touch.drawer = false
			return ""
		}
		if m.touch.layout == layoutCompact && !m.Detail {
			if dx < 0 {
				return "right"
			}
			return "left"
		}
		if m.Detail && dx > 0 {
			// A swipe from the left goes back, as on a phone.
			return "touch:back"
		}
		return ""
	}
	if m.Detail || m.touch.layout == layoutRegular {
		return "wheel-" + map[bool]string{true: "up", false: "down"}[dy > 0]
	}
	if dy > 0 && m.touch.top == 0 {
		// Pulled down at the top of the list: refresh.
		return "r"
	}
	m.scrollList(-dy / touchRows)
	return ""
}

// deviceMouse turns a mouse report into a gesture while the simulator runs.
func (m *Model) deviceMouse(k string) string {
	p := strings.Split(k, ":")
	if len(p) != 5 {
		return ""
	}
	b, e1 := strconv.Atoi(p[1])
	x, e2 := strconv.Atoi(p[2])
	y, e3 := strconv.Atoi(p[3])
	if e1 != nil || e2 != nil || e3 != nil {
		return ""
	}
	return m.gesture(b, x-1, y-1, p[4] == "m")
}

// --- the system's drawing -----------------------------------------------------

// system draws what the device's own system draws over the TUI: the time
// and the status icons, in the status bar, iPhone Duo's column or its
// status cluster; the Dynamic Island and the cameras; the home indicator.
func (m *Model) system(cv *Canvas, s display) {
	clock := m.Now.Format("15:04")
	icons := "▂▄▆█ ▕██▏"
	text := fg(cText).bold()
	switch {
	case !s.column.empty():
		// The column: under the cover's camera, or at the top of the open
		// phone's, the time over the status icons.
		c := s.clock
		cv.Fill(c.x, c.y, c.w, c.h, Style{BG: cInkHi})
		y := c.y + 1
		if len(s.occlusions) > 1 {
			y = s.occlusions[1].bottom() + 1
		}
		cv.Text(c.x+(c.w-width(clock))/2, y, clock, text.Bg(cInkHi), -1)
		cv.Text(c.x+(c.w-width(icons))/2, y+2, icons, fg(cText).Bg(cInkHi), -1)
		cv.Text(c.x+(c.w-5)/2, y+3, m.Now.Format("Mon 2"), fg(cMuted).Bg(cInkHi), -1)
		for k := c.x + 1; k < c.right()-1; k++ {
			cv.Set(k, c.bottom()-1, '─', fg(cLine).Bg(cInkHi))
		}
	case !s.clock.empty():
		// The open phone upright: the status cluster at the top right, the
		// time at the left of the band.
		y := s.clock.y + s.clock.h/2 - 1
		cv.Text(2, y, clock, text, -1)
		cv.Text(s.clock.right()-width(icons)-2, y, icons, fg(cText), -1)
	case s.page.y > 0:
		y := max(0, s.page.y/2-1)
		if len(s.occlusions) > 0 {
			y = s.occlusions[0].y + s.occlusions[0].h/2
		}
		x := 4
		if s.w > 120 {
			x = 2
		}
		cv.Text(x, y, clock, text, -1)
		cv.Text(s.w-width(icons)-x, y, icons, fg(cText), -1)
	case s.page.x > 0:
		// On its side the status bar goes; the island stays on the left.
	}
	// The home indicator, in the bottom inset.
	if s.page.bottom() < s.h {
		n := min(19, s.w/5)
		for k := 0; k < n; k++ {
			cv.Set((s.w-n)/2+k, s.h-1, '▀', fg(Mix(cText, cInk, .25)))
		}
	}
}
