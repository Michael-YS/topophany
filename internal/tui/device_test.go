//go:build !nodevice

package tui

import (
	"slices"
	"strings"
	"testing"
)

// The device layouts keep the rules iPhone Duo's adaptation guide sets out,
// and a phone's: the crease is a gutter no control stands on, the system's
// drawing is left clear, a target is as tall as a finger, every tab and
// every action is reachable on every screen, bending reflows nothing, and a
// change of pose keeps the viewer's place.

func TestDeviceGeometry(t *testing.T) {
	for _, c := range []struct {
		id                   string
		w, h                 int
		page, crease, column rect
		clock                rect
		layout               string
	}{
		{"iphone", 60, 65, rect{0, 5, 60, 57}, rect{}, rect{}, rect{}, layoutCompact},
		{"iphone-landscape", 130, 30, rect{9, 0, 112, 28}, rect{}, rect{}, rect{}, layoutWide},
		{"duo-cover", 66, 48, rect{0, 0, 54, 46}, rect{}, rect{54, 0, 12, 46}, rect{54, 0, 12, 13}, layoutCompact},
		// The crease, 455.5 to 495.5 pt, covered by the six columns from 455 pt.
		{"duo-open", 135, 47, rect{0, 0, 123, 45}, rect{65, 0, 6, 47}, rect{123, 0, 12, 45}, rect{123, 0, 12, 9}, layoutSpread},
		{"duo-book", 135, 47, rect{0, 0, 123, 45}, rect{65, 0, 6, 47}, rect{123, 0, 12, 45}, rect{123, 0, 12, 9}, layoutSpread},
		// Upright, the four rows from 448 pt.
		{"duo-upright", 95, 67, rect{0, 6, 95, 59}, rect{0, 32, 95, 4}, rect{}, rect{76, 0, 19, 6}, layoutStacked},
		{"duo-laptop", 95, 67, rect{0, 6, 95, 59}, rect{0, 32, 95, 4}, rect{}, rect{76, 0, 19, 6}, layoutStacked},
		{"ipad", 196, 73, rect{0, 2, 196, 70}, rect{}, rect{}, rect{}, layoutRegular},
		{"ipad-portrait", 147, 98, rect{0, 2, 147, 94}, rect{}, rect{}, rect{}, layoutRegular},
	} {
		d, e := DeviceByID(c.id)
		if e != nil {
			t.Fatal(e)
		}
		s := d.display()
		if s.w != c.w || s.h != c.h || s.page != c.page || s.crease != c.crease || s.column != c.column || s.clock != c.clock || s.layout() != c.layout {
			t.Errorf("%s: %d×%d page %+v crease %+v column %+v clock %+v layout %s", c.id, s.w, s.h, s.page, s.crease, s.column, s.clock, s.layout())
		}
	}
	if d, e := DeviceByID("duo"); e != nil || d.ID != "duo-cover" {
		t.Errorf("duo is %v, %v", d, e)
	}
	if _, e := DeviceByID("pixel"); e == nil || !strings.Contains(e.Error(), "duo-laptop") {
		t.Errorf("an unknown device is not refused with the list: %v", e)
	}
}

// screens draws every tab on every device, as it opens and with a row's
// evidence open, and calls check with each frame.
func screens(t *testing.T, check func(t *testing.T, name string, m *Model, s display, cv *Canvas)) {
	for _, d := range Devices() {
		for tab := range tabs {
			for _, detail := range []bool{false, true} {
				r := deviceRecorder(t, d.ID)
				m := r.m
				m.setTab(tab)
				s := m.Device.display()
				m.drawScreen(s)
				if detail {
					m.Key("enter")
				}
				cv := m.drawScreen(s)
				name := d.ID + " " + tabs[tab]
				if detail {
					name += " (evidence open)"
				}
				check(t, name, m, s, cv)
			}
		}
	}
}

func TestDeviceControlsKeepOffTheCreaseAndTheSystem(t *testing.T) {
	screens(t, func(t *testing.T, name string, m *Model, s display, _ *Canvas) {
		reserved := []rect{}
		for _, o := range s.occlusions {
			reserved = append(reserved, o)
		}
		for _, h := range m.hits {
			if h.key == "" {
				continue
			}
			at := rect{h.x, h.y, h.w, h.h}
			if at.overlaps(s.crease) {
				t.Errorf("%s: %q on the crease at %+v", name, h.key, at)
			}
			for _, o := range reserved {
				if at.overlaps(o) {
					t.Errorf("%s: %q under the system's drawing %+v at %+v", name, h.key, o, at)
				}
			}
			inside := func(r rect) bool {
				return at.x >= r.x && at.y >= r.y && at.right() <= r.right() && at.bottom() <= r.bottom()
			}
			if !inside(s.page) && !inside(s.column) {
				t.Errorf("%s: %q outside the safe area at %+v (page %+v)", name, h.key, at, s.page)
			}
		}
	})
}

func TestDeviceTargetsAreAFingerTall(t *testing.T) {
	screens(t, func(t *testing.T, name string, m *Model, s display, _ *Canvas) {
		if s.layout() == layoutRegular {
			// A tablet keeps the terminal's lists, a row each, for its
			// pointer and its keyboard; its own chrome is checked below.
			return
		}
		rows := map[string]map[int]bool{}
		tall := map[string]bool{}
		for _, h := range m.hits {
			control := strings.HasPrefix(h.key, "touch:") || strings.HasPrefix(h.key, "key:") || strings.HasPrefix(h.key, "row:")
			if !control {
				continue // links in what is read: a node on the map, a service by name
			}
			if rows[h.key] == nil {
				rows[h.key] = map[int]bool{}
			}
			for y := h.y; y < h.y+h.h; y++ {
				rows[h.key][y] = true
			}
			tall[h.key] = tall[h.key] || h.h >= touchRows
		}
		for k, ys := range rows {
			if !tall[k] && len(ys) < touchRows {
				t.Errorf("%s: %q is %d rows tall", name, k, len(ys))
			}
		}
	})
}

func TestDeviceScreensReachEveryTabAndAction(t *testing.T) {
	screens(t, func(t *testing.T, name string, m *Model, s display, _ *Canvas) {
		keys := map[string]bool{}
		for _, h := range m.hits {
			keys[h.key] = true
		}
		for i := range tabs {
			if !keys["touch:tab:"+itoa(i)] {
				t.Errorf("%s: no way to the %s tab", name, tabs[i])
			}
		}
		if !keys["touch:more"] {
			t.Errorf("%s: no ⋯ with the actions the keys have", name)
		}
	})
	// ⋯ offers every action the footer would, but what a phone's system
	// does itself and what a device, which only views, has no use for.
	r := deviceRecorder(t, "iphone")
	m := r.m
	m.drawScreen(m.Device.display())
	m.Key("touch:more")
	if m.modal == nil || m.modal.kind != "menu" {
		t.Fatal("⋯ opened no menu")
	}
	got := []string{}
	for _, it := range m.modal.items {
		got = append(got, it.label)
	}
	for _, want := range []string{"Details", "Filter", "View", "Refresh", "Scenario", "Map", "Zoom", "Full map", "Settings", "About"} {
		if !slices.Contains(got, want) {
			t.Errorf("⋯ lacks %s: %v", want, got)
		}
	}
	for _, not := range []string{"Quit", "Add", "Edit", "Delete", "Debug"} {
		if slices.Contains(got, not) {
			t.Errorf("⋯ offers %s: %v", not, got)
		}
	}
}

func TestBendingReflowsNothing(t *testing.T) {
	for _, pair := range [][2]string{{"duo-open", "duo-book"}, {"duo-upright", "duo-laptop"}} {
		for tab := range tabs {
			var frames [2]string
			for i, id := range pair {
				r := deviceRecorder(t, id)
				r.m.setTab(tab)
				frames[i] = strings.Join(r.m.drawScreen(r.m.Device.display()).Lines(colorTrue), "\n")
			}
			if frames[0] != frames[1] {
				t.Errorf("%s: %s and %s are laid out differently", tabs[tab], pair[0], pair[1])
			}
		}
	}
}

func TestPosesKeepThePlace(t *testing.T) {
	r := deviceRecorder(t, "duo-cover")
	m := r.m
	m.setTab(tabIncidents)
	m.Selected = 2
	m.Key("enter")
	for range len(m.Device.poses()) + 1 {
		m.Key("]")
		m.drawScreen(m.Device.display())
		if m.Tab != tabIncidents || m.Selected != 2 || !m.Detail {
			t.Fatalf("%s: tab %d, row %d, evidence %v", m.Device.ID, m.Tab, m.Selected, m.Detail)
		}
	}
	m.Key("\\")
	if m.Device.Model != "iPad Pro 13″" || m.Tab != tabIncidents || m.Selected != 2 {
		t.Fatalf("the next device is %s, tab %d, row %d", m.Device.ID, m.Tab, m.Selected)
	}
}

// at finds where the simulator drew the target key, in the terminal's
// 1-based cells, as a mouse would report it.
func at(t *testing.T, m *Model, key string) (int, int) {
	t.Helper()
	for _, h := range m.hits {
		if h.key == key {
			return h.x + h.w/2 + 1, h.y + h.h/2 + 1
		}
	}
	t.Fatalf("no %q drawn", key)
	return 0, 0
}

func TestSimulatorTurnsTheMouseIntoAFinger(t *testing.T) {
	r := deviceRecorder(t, "iphone")
	m := r.m
	m.Width, m.Height = 120, 72
	press := func(b, x, y int, up bool) string {
		end := "M"
		if up {
			end = "m"
		}
		return m.Key("mouse:" + itoa(b) + ":" + itoa(x) + ":" + itoa(y) + ":" + end)
	}
	tap := func(x, y int) { press(0, x, y, false); press(0, x, y, true) }
	if m.draw() == nil {
		t.Fatal("the simulator drew nothing")
	}
	// A press alone does nothing; the tap is the lift, where it went down.
	x, y := at(t, m, "touch:tab:6")
	press(0, x, y, false)
	if m.Tab != tabOverview {
		t.Fatal("a press acted before the finger lifted")
	}
	press(0, x, y, true)
	if m.Tab != tabIncidents {
		t.Fatalf("a tap on Incidents left tab %d", m.Tab)
	}
	// A tap opens a row on a phone, as a pushed page with a way back.
	m.draw()
	x, y = at(t, m, "touch:open:1")
	tap(x, y)
	if !m.Detail || m.Selected != 1 {
		t.Fatalf("a tap on the second incident: row %d, evidence %v", m.Selected, m.Detail)
	}
	m.draw()
	x, y = at(t, m, "touch:back")
	tap(x, y)
	if m.Detail {
		t.Fatal("Back left the evidence open")
	}
	// On the map the wheel pinches, and a drag moves the magnified map.
	m.setTab(tabTopology)
	m.draw()
	mr, o := m.touch.mapRect, m.touch.origin
	mx, my := o[0]+mr.x+mr.w/2+1, o[1]+mr.y+mr.h/2+1
	press(64, mx, my, false)
	if m.touch.zoom != 1 {
		t.Fatalf("a pinch left the zoom at %d", m.touch.zoom)
	}
	m.draw()
	before := m.touch.pan
	press(0, mx, my, false)
	press(0, mx-6, my-3, true)
	m.draw()
	if !m.touch.panned || m.touch.pan == before {
		t.Fatalf("a drag left the map at %v (from %v)", m.touch.pan, before)
	}
	// Across the page a swipe turns to the next tab; from the edge, the
	// drawer comes out.
	m.setTab(tabServices)
	m.draw()
	press(0, o[0]+40, o[1]+40, false)
	press(0, o[0]+20, o[1]+41, true)
	if m.Tab != tabPaths {
		t.Fatalf("a swipe to the left left tab %d", m.Tab)
	}
	m.draw()
	press(0, o[0]+1, o[1]+40, false)
	press(0, o[0]+25, o[1]+40, true)
	if !m.touch.drawer {
		t.Fatal("a swipe from the edge left the drawer shut")
	}
	// Pulled down at the top, a list refreshes.
	m.touch.drawer = false
	m.draw()
	press(0, o[0]+40, o[1]+30, false)
	if got := press(0, o[0]+40, o[1]+40, true); got != "refresh" {
		t.Fatalf("a pull at the top of the list: %q", got)
	}
}

func TestSimulatorSaysWhenTheTerminalIsTooSmall(t *testing.T) {
	r := deviceRecorder(t, "duo-upright")
	m := r.m
	m.Width, m.Height = 80, 24
	text := strings.Join(m.draw().Lines(colorNone), "\n")
	if !strings.Contains(text, "needs 99×70 cells") || !strings.Contains(text, "SIMULATOR") {
		t.Fatalf("too small a terminal:\n%s", text)
	}
}
