package tui

import (
	"strings"
	"testing"
	"time"
)

// f gives the body to the map alone and back: the list and the evidence go,
// the foot of the map names the selection, and Esc or f brings the panels
// back. From a tab without a map, f opens the Topology's map alone.
func TestFullMapHidesThePanels(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 160, 45
	m.setTab(tabTopology)
	if out := visible(m); !strings.Contains(out, "NODES · LINKS") || !strings.Contains(out, "Full map") {
		t.Fatal("panels or the f key missing", out)
	}
	m.Key("f")
	out := visible(m)
	if !m.FullMap || strings.Contains(out, "NODES · LINKS") || strings.Contains(out, "EVIDENCE") {
		t.Fatal("f left the panels", out)
	}
	sel := m.Rows()[m.Selected]
	lines := strings.Split(out, "\n")
	if foot := lines[m.Height-2]; !strings.Contains(foot, sel.Title) || !strings.Contains(foot, "1/") {
		t.Fatal("the foot does not name the selection", foot)
	}
	if !strings.Contains(out, "f panels") || !strings.Contains(out, "Panels") {
		t.Fatal("no way back is shown", out)
	}
	// The selection still moves, and Enter still opens its evidence.
	m.Key("down")
	if m.Selected != 1 || !strings.Contains(strings.Split(visible(m), "\n")[m.Height-2], m.Rows()[1].Title) {
		t.Fatal("selection does not move with the map alone")
	}
	m.Key("enter")
	if !m.Detail || !strings.Contains(visible(m), "Esc/Enter back") {
		t.Fatal("Enter does not open the evidence")
	}
	m.Key("esc")
	if m.Detail || !m.FullMap {
		t.Fatal("Esc should leave the evidence for the map alone")
	}
	m.Key("esc")
	if m.FullMap {
		t.Fatal("Esc does not bring the panels back")
	}
	m.setTab(tabServers)
	m.Key("f")
	if m.Tab != tabTopology || !m.FullMap {
		t.Fatal("f from Servers", m.Tab, m.FullMap)
	}
	// The Overview's map goes alone too, at any size.
	m.setTab(tabOverview)
	m.Width, m.Height = 80, 24
	if out := visible(m); strings.Contains(out, "STATUS") || !strings.Contains(out, "OVERVIEW · ") || !strings.Contains(out, "CHECKS") {
		t.Fatal("the Overview kept its cards, or lost its counts with them", out)
	}
	if m.Animating() > 140*time.Millisecond {
		t.Fatal("the map alone does not animate")
	}
}

// Labels take the room there is: a card or a chip shows its whole name when
// the space beside it is free, rather than stopping at a fixed width.
func TestLabelsUseTheRoomTheyHave(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabTopology)
	// The network graph gives a column to every step from an observer to what
	// it reached, a reported colo among them, so with the home observers'
	// trace checks it needs a wider terminal than the virtual map to keep
	// every name; the map panel is the columns left of the list.
	for _, tc := range []struct {
		mode        string
		width, cols int
		names       []string
	}{
		{"virtual", 211, 128, []string{"Azure 纽约", "互联网 (出口校验目标)", "Oracle 纽约"}},
		{"network", 225, 136, []string{"PostgreSQL 容器", "OpenList 容器"}},
	} {
		m.Width, m.Height, m.MapMode = tc.width, 50, tc.mode
		out := mapText(m, tc.cols)
		for _, n := range tc.names {
			if !strings.Contains(out, n) {
				t.Fatalf("%s map cuts %q short:\n%s", tc.mode, n, out)
			}
		}
	}
	m.Width, m.Height = 211, 50
	// With the map alone, the network graph has room for every name.
	m.FullMap = true
	out := visible(m)
	for _, n := range []string{"Windows 台式机", "Oracle 纽约", "Oracle 洛杉矶", "互联网 (出口校验目标)"} {
		if !strings.Contains(out, n) {
			t.Fatalf("full network map cuts %q short:\n%s", n, out)
		}
	}
}

// With the map to itself, the virtual geo says what runs on each node, on a
// line under it, trouble first; with the panels, it does not.
func TestFullMapSaysWhatRunsOnEachNode(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 211, 50
	m.setTab(tabTopology)
	m.MapMode = "virtual"
	if out := mapText(m, 128); strings.Contains(out, "◆") || strings.Contains(out, "Clash Verge") {
		t.Fatal("services on the map beside the panels", out)
	}
	m.FullMap = true
	out := visible(m)
	for _, want := range []string{"◆ Clash Verge · Windows", "◆ Clash Verge · MacBook", "◆ PostgreSQL"} {
		if !strings.Contains(out, want) {
			t.Fatalf("full map lacks %q:\n%s", want, out)
		}
	}
	runs := m.nodeServices()
	for node, rs := range runs {
		for i := 1; i < len(rs); i++ {
			if rs[i-1].st.FG == runQuiet && rs[i].st.FG != runQuiet {
				t.Fatal("trouble listed after what is fine on", node)
			}
		}
	}
}
