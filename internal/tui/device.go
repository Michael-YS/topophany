//go:build !nodevice

package tui

import (
	"errors"
	"math"
	"strings"
)

// Devices: the phone and tablet screens the TUI lays itself out for (topo
// --device). A device's window and what its system reserves in it (the
// safe-area insets, the Dynamic Island, iPhone Duo's clock column and its
// crease) are given in points. On a device a cell is cellW points wide and
// twice as tall (a monospaced font at about 11.7 pt, the homepage's 2:1
// cell), so each region becomes whole cells: the page inside the insets
// rounded in, what the system covers rounded out.
//
// The iPhone Duo figures are those measured in
// https://foldhunt.com/guides/everything-your-agent-needs-for-iphone-duo/.
//
// The devices, their layouts and the simulator are left out of a build
// with -tags nodevice (device_off.go).

const cellW, cellH = 7.0, 14.0 // points per cell

// Region is a rectangle in points.
type Region struct{ X, Y, W, H float64 }

// Device is one device's screen in one pose.
type Device struct {
	ID     string     // the model, then the pose: "duo-book"
	Model  string     // "iPhone Duo"
	Pose   string     // "book"
	About  string     // how the device is held
	Size   [2]float64 // the window, points
	Insets [4]float64 // the safe-area insets, points: top, right, bottom, left
	// Occlusions are the system's own drawing over the window: the
	// Dynamic Island, the clock column, the status cluster, a camera.
	Occlusions []Region
	// Crease is where the display folds, nil where it cannot. Part folded
	// it is active and bends; lying flat it is only where it will, and a
	// layout puts its gutter there anyway, so bending reflows nothing.
	Crease       *Region
	CreaseActive bool
	// Column is the edge where the system draws its vertical bar, the
	// clock at its top ("trailing"), or "" where it draws none.
	Column string
	Scale  float64 // pixels per point
	Angle  float64 // the hinge's opening, degrees; 180 lying flat
}

// devices are the screens the TUI is laid out for, each model's poses in
// the order they unfold.
var devices = []Device{
	{ID: "iphone", Model: "iPhone 6.5″", Pose: "portrait", About: "upright, a 6.5-inch iPhone such as iPhone Air",
		Size: [2]float64{420, 912}, Insets: [4]float64{62, 0, 34, 0},
		Occlusions: []Region{{147, 11, 126, 37}}, Scale: 3, Angle: 180},
	{ID: "iphone-landscape", Model: "iPhone 6.5″", Pose: "landscape", About: "turned on its side, the Dynamic Island on the left",
		Size: [2]float64{912, 420}, Insets: [4]float64{0, 62, 21, 62},
		Occlusions: []Region{{11, 147, 37, 126}}, Scale: 3, Angle: 180},
	{ID: "duo-cover", Model: "iPhone Duo", Pose: "cover", About: "shut, on the cover display",
		Size: [2]float64{466, 678}, Insets: [4]float64{0, 84, 34, 0},
		Occlusions: []Region{{382, 0, 84, 170}, {399.7, 29.3, 37, 37}}, Column: "trailing", Scale: 3, Angle: 0},
	{ID: "duo-open", Model: "iPhone Duo", Pose: "open", About: "open sideways, lying flat",
		Size: [2]float64{951, 669}, Insets: [4]float64{0, 84, 34, 0},
		Occlusions: []Region{{867, 0, 84, 120}}, Crease: &Region{455.5, 0, 40, 669}, Column: "trailing", Scale: 3, Angle: 180},
	{ID: "duo-book", Model: "iPhone Duo", Pose: "book", About: "open sideways, part folded like a book (128°)",
		Size: [2]float64{951, 669}, Insets: [4]float64{0, 84, 34, 0},
		Occlusions: []Region{{867, 0, 84, 120}}, Crease: &Region{455.5, 0, 40, 669}, CreaseActive: true, Column: "trailing", Scale: 3, Angle: 128},
	{ID: "duo-upright", Model: "iPhone Duo", Pose: "upright", About: "open upright, lying flat",
		Size: [2]float64{669, 951}, Insets: [4]float64{82, 0, 34, 0},
		Occlusions: []Region{{535, 0, 134, 82}}, Crease: &Region{0, 455.5, 669, 40}, Scale: 3, Angle: 180},
	{ID: "duo-laptop", Model: "iPhone Duo", Pose: "laptop", About: "open upright, part folded like a laptop (110°)",
		Size: [2]float64{669, 951}, Insets: [4]float64{82, 0, 34, 0},
		Occlusions: []Region{{535, 0, 134, 82}}, Crease: &Region{0, 455.5, 669, 40}, CreaseActive: true, Scale: 3, Angle: 110},
	{ID: "ipad", Model: "iPad Pro 13″", Pose: "landscape", About: "landscape, full screen",
		Size: [2]float64{1376, 1032}, Insets: [4]float64{24, 0, 20, 0}, Scale: 2, Angle: 180},
	{ID: "ipad-portrait", Model: "iPad Pro 13″", Pose: "portrait", About: "portrait, full screen",
		Size: [2]float64{1032, 1376}, Insets: [4]float64{24, 0, 20, 0}, Scale: 2, Angle: 180},
}

// Devices lists the screens the TUI is laid out for.
func Devices() []Device { return append([]Device{}, devices...) }

// DeviceByID finds a device by its ID, or by its model's first word
// ("iphone", "duo", "ipad") for that model's first pose.
func DeviceByID(id string) (*Device, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	for i := range devices {
		if devices[i].ID == id {
			d := devices[i]
			return &d, nil
		}
	}
	for i := range devices {
		if strings.HasPrefix(devices[i].ID, id+"-") {
			d := devices[i]
			return &d, nil
		}
	}
	ids := []string{}
	for _, d := range devices {
		ids = append(ids, d.ID)
	}
	return nil, errors.New("no device " + id + "; one of " + strings.Join(ids, ", "))
}

// poses are the device's model in each of its poses.
func (d Device) poses() []Device {
	var out []Device
	for _, o := range devices {
		if o.Model == d.Model {
			out = append(out, o)
		}
	}
	return out
}

// display is a device's window in cells, and what it reserves.
type display struct {
	Device
	w, h       int
	page       rect   // inside the safe-area insets
	crease     rect   // the fold's cells; empty where the display does not fold
	column     rect   // the system's column for a vertical bar; empty where none
	clock      rect   // what the system draws at the column's top, or its status cluster
	occlusions []rect // the system's drawing over the window
}

// cellSpan turns the points from a to b on an axis into the cells that cover
// them, rounded out.
func cellSpan(a, b, cell float64, n int) (int, int) {
	lo := max(0, int(math.Floor(a/cell+1e-9)))
	hi := min(n, int(math.Ceil(b/cell-1e-9)))
	return lo, max(lo, hi)
}

func (d Device) cells(r Region, w, h int) rect {
	x0, x1 := cellSpan(r.X, r.X+r.W, cellW, w)
	y0, y1 := cellSpan(r.Y, r.Y+r.H, cellH, h)
	return rect{x0, y0, x1 - x0, y1 - y0}
}

// display measures the device in cells.
func (d Device) display() display {
	s := display{Device: d}
	s.w = int(d.Size[0] / cellW)
	s.h = int(d.Size[1] / cellH)
	top, right, bottom, left := d.Insets[0], d.Insets[1], d.Insets[2], d.Insets[3]
	x0 := int(math.Ceil(left/cellW - 1e-9))
	y0 := int(math.Ceil(top/cellH - 1e-9))
	x1 := min(s.w, int(math.Floor((d.Size[0]-right)/cellW+1e-9)))
	y1 := min(s.h, int(math.Floor((d.Size[1]-bottom)/cellH+1e-9)))
	s.page = rect{x0, y0, x1 - x0, y1 - y0}
	for _, o := range d.Occlusions {
		s.occlusions = append(s.occlusions, d.cells(o, s.w, s.h))
	}
	if d.Crease != nil {
		s.crease = d.cells(*d.Crease, s.w, s.h)
	}
	switch d.Column {
	case "trailing":
		s.column = rect{x1, 0, s.w - x1, y1}
	case "leading":
		s.column = rect{0, 0, x0, y1}
	}
	if len(s.occlusions) > 0 && (!s.column.empty() || d.Insets[0] > 0 && s.occlusions[0].x > s.w/2) {
		s.clock = s.occlusions[0]
	}
	return s
}

// The layouts a screen gets, from its shape and its crease.
const (
	layoutCompact = "compact" // a phone's page: one column, the tabs in a rail
	layoutWide    = "wide"    // a phone on its side: two panes beside a rail
	layoutSpread  = "spread"  // a crease down the page: a leaf each side of it
	layoutStacked = "stacked" // a crease across the page: a half above, a half below
	layoutRegular = "regular" // a tablet: the terminal's own layouts, touch chrome around them
)

// layout is how the screen's page is laid out. The decision is the page's
// shape and where the crease runs, never the pose's name, so a pose with
// the same geometry gets the same layout.
func (s display) layout() string {
	p := s.page
	switch {
	case !s.crease.empty() && s.crease.h >= p.h && p.w > 2*p.h:
		return layoutSpread
	case !s.crease.empty() && s.crease.w >= p.w:
		return layoutStacked
	case p.w < 80:
		return layoutCompact
	case p.h < 36:
		return layoutWide
	}
	return layoutRegular
}
