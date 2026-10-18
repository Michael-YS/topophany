package tui

import (
	"math"
	"strings"
	"testing"
)

// Fitting sites on both sides of the Pacific folds the ocean between them
// into a seam: the sites get more of the width, every one stays in view and
// where a click finds it, and the seam itself is blank. The focus and world
// views stay true to the globe.
func TestGeoFoldsTheEmptyOceanBetweenSites(t *testing.T) {
	pts := [][2]float64{{30.6, 114.3}, {30.3, 120.2}, {35.7, 139.7}, {34.05, -118.24}, {37.3, -121.9}}
	folded := fitGeo(pts, 400, 120, 1.3, 0, nil)
	plain := frameGeo(pts, 400, 120, 1.3, 0, nil, false)
	if len(folded.folds) != 1 || len(plain.folds) != 0 {
		t.Fatal("folds", folded.folds, plain.folds)
	}
	if gain := plain.scale / folded.scale; gain < 1.4 {
		t.Fatal("folding gained the sites too little room", gain)
	}
	for _, p := range pts {
		x, y := folded.project(p[0], p[1])
		if x < 0 || x >= 400 || y < 0 || y >= 120 {
			t.Fatal("point outside view", p, x, y)
		}
		if lat, lon, ok := folded.at(x, y); !ok || math.Abs(lat-p[0]) > 1e-6 || math.Abs(math.Mod(lon-p[1]+540, 360)-180) > 1e-6 {
			t.Fatal("at does not invert project", p, lat, lon, ok)
		}
	}
	f := folded.folds[0]
	if f.b-f.a < 60 {
		t.Fatal("the fold should hold most of the Pacific", f)
	}
	mid := (folded.squeeze(f.a) + f.seam/2) * folded.kx / folded.scale
	if _, _, ok := folded.at(mid, 40); ok {
		t.Fatal("the seam should be blank")
	}
	for s := 0.0; s < 300; s += 7 {
		if d := folded.unsqueeze(folded.squeeze(s)); math.Abs(d-s) > 1e-9 {
			t.Fatal("unsqueeze does not invert squeeze", s, d)
		}
	}
	if g := fitGeo(pts, 400, 120, 1.3, 2, nil); len(g.folds) != 0 {
		t.Fatal("the world view folded", g.folds)
	}
	if g := fitGeo(pts[:3], 400, 120, 1.3, 0, nil); len(g.folds) != 0 {
		t.Fatal("sites without a wide gap folded", g.folds)
	}
}

// Every site gets a label, no two labels touch, none covers a marker, and
// moving the selection moves none of them.
func TestGeoPlatesKeepClearOfEachOther(t *testing.T) {
	for _, labels := range []geoLabels{geoCards, geoLocations} {
		for _, dim := range [][2]int{{211, 50}, {140, 40}, {100, 30}} {
			m := previewModel(t)
			m.Width, m.Height = dim[0], dim[1]
			m.setTab(tabTopology)
			m.MapMode = "geo"
			m.GeoLabels = labels
			out := visible(m)
			if dim[0] >= 140 && !strings.Contains(out, "° folded") {
				t.Fatal("no fold over the oceans", dim)
			}
			ps := append([]plate(nil), m.pcache.plates...)
			sites := map[int]bool{}
			for i, a := range ps {
				if labels == geoLocations && (!a.location || a.h != 1 || len(a.rows) != 0) {
					t.Fatal("location label contains card detail", a.title)
				}
				sites[a.k] = true
				for _, b := range ps[i+1:] {
					if a.x-1 < b.x+b.w && b.x-1 < a.x+a.w && a.y-1 < b.y+b.h && b.y-1 < a.y+a.h {
						t.Fatalf("%v: labels %q and %q touch", dim, a.title, b.title)
					}
				}
				for _, b := range ps {
					if cx, cy := int(b.site.x)/2, int(b.site.y)/4; cx >= a.x && cx < a.x+a.w && cy >= a.y && cy < a.y+a.h {
						t.Fatalf("%v: label %q covers the marker of %q", dim, a.title, b.title)
					}
				}
			}
			if dim[0] == 211 {
				if len(sites) != 7 { // the preview's seven places
					t.Fatal("sites without a label", len(sites))
				}
				for _, p := range ps {
					if p.line && labels == geoCards {
						t.Fatal("a roomy map folded a label to one line", p.title)
					}
				}
			}
			m.Key("down")
			m.Key("down")
			m.Render()
			for i, p := range m.pcache.plates {
				if p.x != ps[i].x || p.y != ps[i].y || p.w != ps[i].w || p.h != ps[i].h {
					t.Fatal("the selection moved a label", p.title)
				}
			}
		}
	}
}

func TestGeoLocationsUsesCoordinatesAndMonochromeFocus(t *testing.T) {
	m := &Model{GeoLabels: geoLocations, NoColor: true}
	nodes := []mapNode{{ID: "unnamed", Name: "Hidden node name", Located: true, Lat: 12.5, Lon: -45.5}}
	cv := NewCanvas(100, 24)
	m.drawGeo(cv, 0, 0, 100, 24, nodes, nil, mapFocus{node: "unnamed"})
	out := securityCanvasText(cv)
	if !strings.Contains(out, coords(12.5, -45.5)) || strings.Contains(out, "Hidden node name") || !strings.Contains(out, "›") {
		t.Fatal("unnamed location or monochrome selection missing", out)
	}
}

func TestGeoControlsStayInsideNarrowMap(t *testing.T) {
	for _, w := range []int{8, 16, 24, 32} {
		m := &Model{GeoLabels: geoLocations}
		cv := NewCanvas(w+20, 12)
		nodes := []mapNode{{ID: "site", Located: true, Lat: 12.5, Lon: -45.5}}
		m.drawGeo(cv, 10, 0, w, 12, nodes, nil, mapFocus{})
		for x := 0; x < cv.W; x++ {
			if (x < 10 || x >= 10+w) && cv.at(x, 0).ch != " " {
				t.Fatal("map controls overwrote an adjacent panel", w, x)
			}
		}
		for _, h := range m.hits {
			if h.x < 10 || h.x+h.w > 10+w {
				t.Fatal("map target extends into an adjacent panel", w, h)
			}
		}
	}
}

func TestGeoLocationsClickAndModeControl(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height, m.FullMap = 160, 45, true
	m.setTab(tabTopology)
	m.MapMode = "geo"
	m.Render()
	clicked := false
	for _, h := range m.hits {
		if h.key == "key:p" && h.w == width(" Cards · p ") {
			m.Key("mouse:0:" + itoa(h.x+1) + ":" + itoa(h.y+1) + ":M")
			clicked = true
			break
		}
	}
	if !clicked || m.GeoLabels != geoLocations || m.Zoom != 0 {
		t.Fatal("map label control did not cycle independently of zoom")
	}
	m.Render()
	ps := m.pcache.plates
	if len(ps) == 0 {
		t.Fatal("full map has no locations")
	}
	p := ps[len(ps)-1]
	for _, h := range m.hits {
		if strings.HasPrefix(h.key, "select:node:") && h.w == p.w {
			id := strings.TrimPrefix(h.key, "select:node:")
			m.Key("mouse:0:" + itoa(h.x+1) + ":" + itoa(h.y+1) + ":M")
			if r, ok := m.selectedRow(); !ok || r.Kind != "node" || r.ID != id {
				t.Fatal("location label did not select its node")
			}
			return
		}
	}
	t.Fatal("location label has no click target")
}

// geoPanel is the geographic map from its title through the legend, cut
// before the list beside it, so a name in the list is not a label on the map.
func geoPanel(t *testing.T, m *Model) string {
	t.Helper()
	lines := strings.Split(visible(m), "\n")
	start, cut := -1, 0
	for i, l := range lines {
		if start < 0 && strings.Contains(l, "geo map") {
			start = i
			if j := strings.Index(l, "NODES"); j > 0 {
				cut = width(l[:j])
			}
			continue
		}
		if start >= 0 && strings.Contains(l, "no evidence") {
			out := []string{}
			for _, row := range lines[start : i+1] {
				if cut > 0 {
					row = clipCols(row, cut)
				}
				out = append(out, row)
			}
			return strings.Join(out, "\n")
		}
	}
	t.Fatal("no geo map panel")
	return ""
}

func clipCols(s string, cols int) string {
	w, b := 0, strings.Builder{}
	for _, r := range s {
		if w+runeWidth(r) > cols {
			break
		}
		w += runeWidth(r)
		b.WriteRune(r)
	}
	return b.String()
}

// p cycles cards, geographic labels and bare points, only on the geo map.
func TestGeoLabelsCycle(t *testing.T) {
	if arcDash(0, false) != 0 || arcDash(1, false) != 1 || arcDash(2, true) != 2 || arcDash(0, true) != 2 || arcDash(1, true) != 2 {
		t.Fatal("arc dash")
	}
	m := previewModel(t)
	m.Width, m.Height = 211, 50
	m.setTab(tabTopology)
	m.MapMode = "geo"
	out := mapText(m, 128)
	for _, want := range []string{"▎ 成都", "▎ 东京", "Locations"} {
		if !strings.Contains(visible(m), want) && !strings.Contains(out, want) {
			t.Fatalf("geo map lacks %q before p:\n%s", want, out)
		}
	}
	if !strings.Contains(visible(m), "Locations") {
		t.Fatal("geo footer lacks p")
	}
	m.Key("p")
	if m.GeoLabels != geoLocations {
		t.Fatal("p did not select locations")
	}
	panel := geoPanel(t, m)
	for _, want := range []string{"成都", "东京", "Locations · p"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("locations view lacks %q:\n%s", want, panel)
		}
	}
	for _, gone := range []string{"▎", "Windows 台式机", "OpenList 容器"} {
		if strings.Contains(panel, gone) {
			t.Fatalf("locations view includes node detail %q:\n%s", gone, panel)
		}
	}
	m.Key("p")
	if m.GeoLabels != geoPoints {
		t.Fatal("second p did not select points")
	}
	panel = geoPanel(t, m)
	for _, gone := range []string{"▎", "成都", "东京", "Windows 台式机", "OpenList 容器"} {
		if strings.Contains(panel, gone) {
			t.Fatalf("points view still shows %q:\n%s", gone, panel)
		}
	}
	// The legend accounts for one of each glyph; the sites add the rest.
	if n := strings.Count(panel, "●") + strings.Count(panel, "○") + strings.Count(panel, "▲") + strings.Count(panel, "✖"); n < 8 {
		t.Fatal("points view dropped the markers", n)
	}
	if !strings.Contains(panel, "┄ path ok") || !strings.Contains(panel, "⠅") {
		t.Fatal("points view lost the dashed legend or the map", panel)
	}
	foot := visible(m)
	if !strings.Contains(foot, "Cards") {
		t.Fatal("no way back to the plates", foot[len(foot)-400:])
	}
	m.Key("p")
	if m.GeoLabels != geoCards || !strings.Contains(geoPanel(t, m), "▎ 成都") {
		t.Fatal("p did not bring the plates back")
	}
	m.MapMode = "virtual"
	before := mapText(m, 128)
	m.Key("p")
	if m.GeoLabels != geoCards || mapText(m, 128) != before || strings.Contains(visible(m), "Points") {
		t.Fatal("p acted on the virtual geo")
	}
	m.MapMode = "network"
	m.Key("p")
	if m.GeoLabels != geoCards || strings.Contains(visible(m), "Points") {
		t.Fatal("p acted on the network graph")
	}
	m.MapMode = "geo"
	m.setTab(tabServices)
	m.Key("p")
	if m.GeoLabels != geoCards {
		t.Fatal("p acted with the geo map off screen")
	}
}

// A site crowded with nodes lists them under its place, the selected node
// lit, and when the map is short says how many it leaves out.
func TestGeoPlateListsTheNodesOfItsPlace(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 211, 50
	m.setTab(tabTopology)
	m.MapMode = "geo"
	out := mapText(m, 128)
	for _, want := range []string{"▎ 成都", "▎ ● Windows 台式机", "▎ ● MacBook Pro", "▎ ● 家庭路由器", "▎ 东京", "▎ └ ● OpenList 容器"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plate lacks %q:\n%s", want, out)
		}
	}
	nodes, _, _ := m.graph()
	sites := geoSites(nodes, fitGeo(nil, 100, 100, 1, 0, nil), mapFocus{})
	for _, s := range sites {
		if len(s.nodes) >= 3 {
			shapes := plateShapes(s, nodes, mapFocus{}, 2, 30, nil, false)
			if shapes[0].more != len(s.nodes)-1 || !shapes[len(shapes)-1].line {
				t.Fatal("a short plate does not count what it leaves out", shapes[0].more)
			}
			return
		}
	}
	t.Fatal("no crowded site in the preview")
}
