//go:build !nodevice

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
)

// The device shots: the TUI on each screen it is laid out for, half an
// hour into the transit outage, as scripts/device_shots.py paints them and
// sets them in the devices' frames. Each is a still in the tour's format
// (<name>.json) beside the device it was drawn for (<name>.device.json).
// Without TOPO_DEVICE_SHOTS the test only checks that each one draws.

type deviceShot struct {
	name     string   // the file's name
	device   string   // the device's ID
	tab      int      // the tab shown
	selected string   // the ID of the row selected, else the first
	keys     []string // pressed once the row is selected, touch controls included
	caption  string   // what the gallery says of it
}

var deviceShots = []deviceShot{
	// A 6.5-inch iPhone: one column, the tabs in a sidebar rail.
	{name: "iphone-overview", device: "iphone", tab: tabOverview,
		caption: "Overview: the verdict, the map a pinch magnifies, then what needs attention, a finger tall each."},
	{name: "iphone-topology", device: "iphone", tab: tabTopology, selected: "transit", keys: []string{"touch:zoom-in"},
		caption: "Topology, the map magnified 1.5× around the selected node; a drag moves the window."},
	{name: "iphone-servers", device: "iphone", tab: tabServers, selected: "app",
		caption: "Servers as short cards, one under another; a tap lays one open."},
	{name: "iphone-incident", device: "iphone", tab: tabIncidents, keys: []string{"touch:open:0"},
		caption: "An incident opened from its list: a page of its own, Back to return."},
	{name: "iphone-checks", device: "iphone", tab: tabChecks,
		caption: "Checks grouped by application, with the filter and Browse over them."},
	{name: "iphone-drawer", device: "iphone", tab: tabOverview, keys: []string{"touch:drawer"},
		caption: "The sidebar's drawer: every tab by name with its count, the view, the scenario, the settings."},
	{name: "iphone-landscape", device: "iphone-landscape", tab: tabOverview,
		caption: "On its side: the rail, the map, and what needs attention beside it."},

	// iPhone Duo through its poses, the same moment in each.
	{name: "duo-cover-overview", device: "duo-cover", tab: tabOverview,
		caption: "Shut: the phone's layout on the cover, the tabs in the clock's column."},
	{name: "duo-cover-servers", device: "duo-cover", tab: tabServers, selected: "app",
		caption: "Shut, Servers: short cards beside the column."},
	{name: "duo-open-overview", device: "duo-open", tab: tabOverview,
		caption: "Open sideways: the map on the left leaf, the verdict and what needs attention on the right; the crease is the gutter."},
	{name: "duo-book-overview", device: "duo-book", tab: tabOverview,
		caption: "Part folded like a book: the same spread, nothing reflowed by the bend."},
	{name: "duo-book-incidents", device: "duo-book", tab: tabIncidents,
		caption: "Book pose, Incidents: the evidence on the left leaf, the list beside the tabs."},
	{name: "duo-upright-overview", device: "duo-upright", tab: tabOverview,
		caption: "Open upright: the map above the hinge, the events and the controls below it."},
	{name: "duo-upright-zoomed", device: "duo-upright", tab: tabOverview, keys: []string{"touch:zoom-in", "touch:zoom-in"},
		caption: "Open upright, the map pinched to 2× around the selected incident's route; the events stay below the hinge."},
	{name: "duo-laptop-overview", device: "duo-laptop", tab: tabOverview,
		caption: "Laptop pose: the map on the raised half to look at, the events and the tabs on the flat half to touch."},
	{name: "duo-laptop-topology", device: "duo-laptop", tab: tabTopology, selected: "transit", keys: []string{"touch:map:geo"},
		caption: "Laptop pose, Topology on the geo map: the map's views and zoom on the flat half, clear of the screen."},

	// iPad: the terminal's own layouts, with touch chrome.
	{name: "ipad-overview", device: "ipad", tab: tabOverview,
		caption: "iPad Pro 13″ landscape: the terminal's wide Overview, with tabs and a toolbar for fingers."},
	{name: "ipad-portrait-servers", device: "ipad-portrait", tab: tabServers,
		caption: "iPad Pro 13″ portrait, Servers: the wall of cards as the terminal draws it."},
}

// deviceSidecar is what scripts/device_shots.py needs to set a shot in its
// device's frame.
type deviceSidecar struct {
	ID           string     `json:"id"`
	Model        string     `json:"model"`
	Pose         string     `json:"pose"`
	About        string     `json:"about"`
	Caption      string     `json:"caption"`
	Size         [2]float64 `json:"size"`
	Scale        float64    `json:"scale"`
	Cells        [2]int     `json:"cells"`
	Cell         [2]float64 `json:"cell"`
	Insets       [4]float64 `json:"insets"`
	Occlusions   []Region   `json:"occlusions"`
	Crease       *Region    `json:"crease"`
	CreaseActive bool       `json:"creaseActive"`
	Angle        float64    `json:"angle"`
	Layout       string     `json:"layout"`
}

// deviceRecorder is the preview half an hour into the transit outage, on
// the device with id.
func deviceRecorder(t *testing.T, id string) *recorder {
	r := newRecorder(t, preview.DefaultLanguage)
	d, e := DeviceByID(id)
	if e != nil {
		t.Fatal(e)
	}
	r.m.Device = d
	r.sim.SetScenario(slices.Index(r.sim.ScenarioIDs(), "transit-blocked"), r.m.Now.Add(-30*time.Minute))
	r.fetch()
	r.m.Now = r.m.Now.Add(4 * time.Second)
	r.m.notice = ""
	return r
}

// shotOn draws c on its device and records it as a still.
func shotOn(t *testing.T, c deviceShot) (tour, deviceSidecar) {
	r := deviceRecorder(t, c.device)
	m := r.m
	m.setTab(c.tab)
	if c.selected != "" {
		m.Selected = slices.IndexFunc(m.Rows(), func(row Row) bool { return row.ID == c.selected })
		if m.Selected < 0 {
			t.Fatalf("%s: no row %q", c.name, c.selected)
		}
	}
	s := m.Device.display()
	m.drawScreen(s) // a frame first, so the keys act on what it drew
	for _, k := range c.keys {
		m.Key(k)
	}
	cv := m.drawScreen(s)
	r.out.Size = [2]int{s.w, s.h}
	r.chapter(c.name)
	r.frame(cv, 0)
	d := *m.Device
	return r.out, deviceSidecar{ID: d.ID, Model: d.Model, Pose: d.Pose, About: d.About, Caption: c.caption,
		Size: d.Size, Scale: d.Scale, Cells: [2]int{s.w, s.h}, Cell: [2]float64{cellW, cellH}, Insets: d.Insets,
		Occlusions: d.Occlusions, Crease: d.Crease, CreaseActive: d.CreaseActive, Angle: d.Angle, Layout: s.layout()}
}

func TestDeviceShots(t *testing.T) {
	dir := os.Getenv("TOPO_DEVICE_SHOTS")
	local := time.Local
	time.Local = time.UTC
	defer func() { time.Local = local }()
	write := func(name string, v any) {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if dir == "" {
			return
		}
		if e := os.WriteFile(filepath.Join(dir, name), b, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	for _, c := range deviceShots {
		out, side := shotOn(t, c)
		write(c.name+".json", out)
		write(c.name+".device.json", side)
	}
}
