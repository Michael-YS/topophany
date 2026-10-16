package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// crowdedModel is a world of many places, several nodes at each, linked
// densely and deterministically, with an edge and the internet unplaced.
func crowdedModel() *Model {
	places := []model.Location{
		{Label: "London", Lat: 51.5, Lon: -0.13}, {Label: "Paris", Lat: 48.86, Lon: 2.35}, {Label: "Frankfurt", Lat: 50.11, Lon: 8.68},
		{Label: "Tokyo", Lat: 35.68, Lon: 139.69}, {Label: "Osaka", Lat: 34.69, Lon: 135.5}, {Label: "Singapore", Lat: 1.35, Lon: 103.82},
		{Label: "Sydney", Lat: -33.87, Lon: 151.21}, {Label: "São Paulo", Lat: -23.55, Lon: -46.63}, {Label: "New York", Lat: 40.71, Lon: -74.01},
		{Label: "Ashburn", Lat: 39.04, Lon: -77.49}, {Label: "Los Angeles", Lat: 34.05, Lon: -118.24}, {Label: "Seattle", Lat: 47.61, Lon: -122.33},
	}
	c := model.Config{}
	for s, p := range places {
		for k := 0; k < 1+s%3; k++ {
			l := p
			c.Nodes = append(c.Nodes, model.Node{ID: strings.ToLower(p.Label[:3]) + itoa(k), Name: p.Label + " " + itoa(k), Kind: "host", Group: "g" + itoa(s%4), Location: &l})
		}
	}
	c.Nodes = append(c.Nodes, model.Node{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare"}, model.Node{ID: "net", Name: "Internet", Kind: "external"})
	for i := range c.Nodes {
		for _, j := range []int{(i*7 + 3) % len(c.Nodes), (i*11 + 5) % len(c.Nodes)} {
			if i != j {
				c.Links = append(c.Links, model.Link{ID: "l" + itoa(len(c.Links)), From: c.Nodes[i].ID, To: c.Nodes[j].ID, Transport: "tcp"})
			}
		}
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	return &Model{Snapshot: engine.Build(c, "primary", "", nil, nil, now), Now: now, Width: 160, Height: 48}
}

func virtualLayout(t *testing.T, m *Model, w, h int) (*vLayout, []mapNode) {
	t.Helper()
	nodes, edges, _ := m.graph()
	l := m.layoutVirtual(w, h, 0, nodes, edges, m.focus(Row{}))
	if l == nil {
		t.Fatal("no layout")
	}
	return l, nodes
}

func cardAt(t *testing.T, l *vLayout, place string) *vCard {
	t.Helper()
	for _, c := range l.cards {
		if c.located && c.place == place {
			return c
		}
	}
	t.Fatal("no card for", place)
	return nil
}

func TestVirtualMapKeepsTheWorldsOrder(t *testing.T) {
	m := previewModel(t)
	l, nodes := virtualLayout(t, m, 127, 42)
	tokyo, la, ny, fra := cardAt(t, l, "东京"), cardAt(t, l, "洛杉矶"), cardAt(t, l, "纽约"), cardAt(t, l, "法兰克福")
	// West to east along the short way round, across the Pacific.
	if !(tokyo.col < la.col && la.col < ny.col && ny.col < fra.col) {
		t.Fatal("columns out of order", tokyo.col, la.col, ny.col, fra.col)
	}
	if hnl := cardAt(t, l, "檀香山"); hnl.col > la.col || hnl.y < la.y {
		t.Fatal("Honolulu should be west of or under Los Angeles", hnl.col, hnl.y, la.col, la.y)
	}
	// North above south, within a column and across the map.
	for _, a := range l.cards {
		for _, b := range l.cards {
			if a.located && b.located && a.col == b.col && a.lat > b.lat && a.y > b.y {
				t.Fatal(a.place, "is north of", b.place, "but drawn below it")
			}
		}
	}
	if !(fra.y < ny.y && ny.y < la.y) {
		t.Fatal("latitude order lost", fra.y, ny.y, la.y)
	}
	// The crowded home site keeps a row for each of its nodes, and the
	// unplaced edges stay on the map as pills.
	home := cardAt(t, l, "成都")
	if len(home.rows) != 3 || home.hidden() != 0 || home.h != 5 {
		t.Fatal("home site folded", len(home.rows), home.hidden(), home.h)
	}
	pills := 0
	for _, c := range l.cards {
		if c.pill() {
			pills++
			if n := nodes[c.rows[0]]; n.Located {
				t.Fatal("located node drawn as a pill", n.ID)
			}
		}
	}
	if pills != 4 {
		t.Fatal("unplaced nodes", pills)
	}
}

// The smallest map the layouts show is about 78×8.
func TestVirtualCardsNeverOverlap(t *testing.T) {
	for name, m := range map[string]*Model{"preview": previewModel(t), "crowded": crowdedModel()} {
		for _, sz := range [][2]int{{78, 8}, {88, 10}, {96, 20}, {96, 38}, {127, 42}, {137, 45}, {200, 60}} {
			l, _ := virtualLayout(t, m, sz[0], sz[1])
			chips := 0
			for i, a := range l.cards {
				if a.chip() {
					chips++
				}
				if a.y < 0 || a.x < 2 || a.x+a.w > sz[0]-2 || a.y+a.h > sz[1] {
					t.Fatalf("%s %v: card %q at %d,%d %dx%d leaves the area", name, sz, a.title(), a.x, a.y, a.w, a.h)
				}
				for _, b := range l.cards[i+1:] {
					if a.x < b.x+b.w && b.x < a.x+a.w && a.y < b.y+b.h && b.y < a.y+a.h {
						t.Fatalf("%s %v: %q and %q overlap", name, sz, a.title(), b.title())
					}
				}
			}
			if sz[1] <= 12 && chips == 0 {
				t.Fatalf("%s %v: nothing folded in a short map", name, sz)
			}
		}
	}
}

// Every link gets a wire, from a port of its source to a port of its
// destination without entering a card. Wires may share cells only when they
// leave the same port or enter the same one, or right beside a node they
// both touch; anywhere else they cross at right angles.
func TestVirtualWiresJoinOnlyAtAnEnd(t *testing.T) {
	for name, m := range map[string]*Model{"preview": previewModel(t), "crowded": crowdedModel()} {
		for _, sz := range [][2]int{{78, 8}, {96, 38}, {110, 40}, {127, 42}, {137, 45}, {160, 45}} {
			nodes, edges, groups := m.graph()
			f := m.focus(Row{})
			m.drawVirtual(NewCanvas(sz[0], sz[1]), 0, 0, sz[0], sz[1], nodes, edges, groups, f)
			l := m.layoutVirtual(sz[0], sz[1], m.vcache.cols, nodes, edges, f)
			r, paths := l.router(), m.vcache.paths
			if m.vcache.dropped > 0 && sz[1] > 8 {
				t.Fatalf("%s %v: %d of %d links have no wire", name, sz, m.vcache.dropped, len(paths))
			}
			ws := newWires(sz[0], sz[1])
			for k, d := range l.drawn {
				p := paths[k]
				if p == nil {
					continue
				}
				if !contains(cellNames(l.ports(d.a)), itoa(p[0])) || !contains(cellNames(l.ports(d.b)), itoa(p[len(p)-1])) {
					t.Fatalf("%s %v: %s does not run port to port", name, sz, d.e.ID)
				}
				for _, c := range p {
					if r.solid[c] {
						t.Fatalf("%s %v: %s runs through a card at %d,%d", name, sz, d.e.ID, c%sz[0], c/sz[0])
					}
				}
				ws.route(corners(p, sz[0]), k, 0, 0, 0)
			}
			for cell, marks := range ws.marks {
				for i, a := range marks {
					for _, b := range marks[i+1:] {
						x, y := l.drawn[a.edge], l.drawn[b.edge]
						if x.pa == y.pa || x.pb == y.pb {
							continue
						}
						if z := r.zone[cell]; z >= 0 && (x.pa == z || x.pb == z) && (y.pa == z || y.pb == z) {
							continue
						}
						if !(a.dirs == wN|wS && b.dirs == wE|wW || a.dirs == wE|wW && b.dirs == wN|wS) {
							t.Fatalf("%s %v: %s and %s meet at %d,%d", name, sz, x.e.ID, y.e.ID, cell%sz[0], cell/sz[0])
						}
					}
				}
			}
		}
	}
}

func cellNames(cells []int) []string {
	out := []string{}
	for _, c := range cells {
		out = append(out, itoa(c))
	}
	return out
}

// mapText is the rendered map panel of the Topology tab: the columns left of
// the list.
func mapText(m *Model, cols int) string {
	out := []string{}
	for _, l := range strings.Split(visible(m), "\n") {
		w, b := 0, strings.Builder{}
		for _, r := range l {
			if w+runeWidth(r) > cols {
				break
			}
			w += runeWidth(r)
			b.WriteRune(r)
		}
		out = append(out, b.String())
	}
	return strings.Join(out, "\n")
}

func TestVirtualMapIsTheDefaultAndShowsPlacesAndLinks(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 211, 50
	m.setTab(tabTopology)
	if m.mapMode() != "virtual" {
		t.Fatal("default map", m.mapMode())
	}
	out := mapText(m, 128)
	for _, s := range []string{"virtual geo", "东京", "洛杉矶", "纽约", "法兰克福", "檀香山", "成都", "杭州", "34°N 118°W", "Windows 台式机", "MacBook Pro", "家庭路由器", "搬瓦工 洛杉矶", "Oracle 洛杉矶", "Cloudflare", "Vercel"} {
		if !strings.Contains(out, s) {
			t.Fatalf("map lacks %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "no location") {
		t.Fatal("unplaced nodes went to a tray")
	}
	lanes := 0
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "搬瓦工 洛杉矶") && strings.Contains(l, "─╮"), strings.Contains(l, "Oracle 洛杉矶") && strings.Contains(l, "◂╯"):
			lanes++
		}
	}
	if lanes != 2 {
		t.Fatal("the chained proxies in Los Angeles are not joined on their card\n" + out)
	}
	if strings.Count(out, "▸")+strings.Count(out, "◂") < 10 || strings.Count(out, "│") < 40 {
		t.Fatal("links missing\n" + out)
	}
	// The selection lights its card, and moving it never moves a card.
	before, _ := virtualLayout(t, m, 127, 42)
	m.Selected = 5
	after, _ := virtualLayout(t, m, 127, 42)
	for i := range before.cards {
		if a, b := before.cards[i], after.cards[i]; a.x != b.x || a.y != b.y || a.w != b.w || a.h != b.h {
			t.Fatal("selection moved a card", a.title())
		}
	}
}

func TestMapModesCycle(t *testing.T) {
	m := previewModel(t)
	for _, want := range []string{"geo", "network", "virtual", "geo"} {
		m.Key("m")
		if m.mapMode() != want {
			t.Fatal("m gave", m.mapMode(), "want", want)
		}
	}
	f := fixtureModel()
	if f.mapMode() != "network" {
		t.Fatal("no locations should default to the network graph", f.mapMode())
	}
	f.MapMode = "virtual"
	f.Width, f.Height = 120, 30
	f.setTab(tabTopology)
	if out := visible(&f); !strings.Contains(out, "No node locations declared") {
		t.Fatal("virtual map without locations", out)
	}
}

func TestVirtualWiresAreKeptBetweenFrames(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 160, 45
	m.setTab(tabTopology)
	m.Render()
	key, paths := m.vcache.key, m.vcache.paths
	if key == 0 || len(paths) == 0 {
		t.Fatal("no wires cached")
	}
	m.Now = m.Now.Add(time.Second)
	m.Render()
	if m.vcache.key != key || &m.vcache.paths[0] != &paths[0] {
		t.Fatal("wires rerouted for an animation frame")
	}
	m.Width = 150
	m.Render()
	if m.vcache.key == key {
		t.Fatal("wires kept across a resize")
	}
}
