package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
)

func previewModel(t *testing.T) *Model {
	t.Helper()
	c, e := preview.Preset()
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sim := preview.New(c, now)
	snap, e := sim.Snapshot("", now)
	if e != nil {
		t.Fatal(e)
	}
	return &Model{Snapshot: snap, Now: now, Width: 160, Height: 48, Preview: sim, Server: "synthetic"}
}

func TestPreviewScenarioMenuDebugAndReadOnly(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabTopology)
	out := visible(m)
	if !strings.Contains(out, "PREVIEW") || !strings.Contains(out, "全部正常") || !strings.Contains(out, "长沙") {
		t.Fatal("preview chrome or geography missing")
	}
	if !strings.Contains(out, "Scenario") || !strings.Contains(out, "Add") {
		t.Fatal("preview footer dropped a key", out[len(out)-400:])
	}
	m.Key("x")
	if m.modal == nil || m.modal.kind != "menu" || len(m.modal.items) < 8 || !strings.Contains(m.modal.items[1].hint, "Clash 回落") {
		t.Fatal("scenario menu", m.modal)
	}
	if m.Key("2") != "refresh" || !strings.Contains(m.notice, "DMIT") {
		t.Fatal("scenario not applied", m.notice)
	}
	m.Key("D")
	if !m.Debug || !strings.Contains(visible(m), "DEBUG") || !strings.Contains(visible(m), "nodes") {
		t.Fatal("debug overlay missing")
	}
	m.Key("D")
	m.Key("a")
	if m.modal == nil || !strings.Contains(visible(m), "print-topology") {
		t.Fatal("preview edit hint missing")
	}
	for _, dim := range [][2]int{{40, 14}, {100, 30}, {160, 48}} {
		for _, mode := range []string{"geo", "network"} {
			for tab := range tabs {
				m.Width, m.Height = dim[0], dim[1]
				m.MapMode = mode
				m.setTab(tab)
				m.Debug = true
				lines := m.Render()
				if len(lines) != dim[1] {
					t.Fatal("height", dim, tab)
				}
				for _, l := range lines {
					if width(ansi.ReplaceAllString(l, "")) != dim[0] {
						t.Fatalf("width %v tab %d: %q", dim, tab, ansi.ReplaceAllString(l, ""))
					}
				}
			}
		}
	}
}

func TestOverviewDashboardAnswersAtAGlance(t *testing.T) {
	c, e := preview.Preset()
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sim := preview.New(c, now)
	snap, _ := sim.Snapshot("", now)
	m := &Model{Snapshot: snap, Now: now, Width: 160, Height: 48, Preview: sim, Server: "synthetic"}
	out := visible(m)
	for _, want := range []string{"ALL CLEAR", "checks passing", "Nothing needs attention", "SERVICES", "OBSERVERS", "长沙"} {
		if !strings.Contains(out, want) {
			t.Fatalf("healthy overview lacks %q", want)
		}
	}
	// DMIT blocked from mainland observers: the verdict, the worst-first
	// attention list and the route of the selected incident.
	sim.SetScenario(1, now.Add(-30*time.Minute))
	m.Snapshot, _ = sim.Snapshot("", now)
	out = visible(m)
	if !strings.Contains(out, "INCIDENTS") || !strings.Contains(out, "checks failing") || !strings.Contains(out, "长沙 MacBook → 主链 DMIT → AAITR 家宽") {
		t.Fatal("blocked overview lacks verdict or incident", out)
	}
	rows := m.Rows()
	if len(rows) == 0 || rows[0].Kind != "incident" {
		t.Fatal("attention list does not lead with incidents", rows)
	}
	for i := 1; i < len(rows); i++ {
		if attentionRank(rows[i]) < attentionRank(rows[i-1]) {
			t.Fatal("attention list not worst first", rows[i-1].Title, rows[i].Title)
		}
	}
	f := m.focus(rows[0])
	if !f.nodes["dmit"] || !f.nodes["aaitr"] || !f.edges["dmit-aaitr"] {
		t.Fatal("selected incident does not light its route", f)
	}
	// A cached view never claims a verdict it cannot know.
	m.Error = "collector unavailable"
	out = visible(m)
	if !strings.Contains(out, "CACHED") || strings.Contains(out, "ALL CLEAR") {
		t.Fatal("cached overview claims a verdict")
	}
}

// replay interprets the SGR stream of one serialized line and reports the
// style in effect for each printed cell.
func replay(t *testing.T, line string) []Style {
	t.Helper()
	out := []Style{}
	cur := Style{}
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		if rs[i] == 0x1b {
			j := i + 2
			for j < len(rs) && rs[j] != 'm' {
				j++
			}
			ps := strings.Split(string(rs[i+2:j]), ";")
			for k := 0; k < len(ps); k++ {
				switch ps[k] {
				case "0":
					cur = Style{}
				case "1", "2", "3", "4", "7":
					cur.Attr |= map[string]uint8{"1": Bold, "2": Faint, "3": Italic, "4": Underline, "7": Reverse}[ps[k]]
				case "38", "48":
					var r, g, b int
					fmt.Sscan(ps[k+2], &r)
					fmt.Sscan(ps[k+3], &g)
					fmt.Sscan(ps[k+4], &b)
					c := Hex(uint32(r<<16 | g<<8 | b))
					if ps[k] == "38" {
						cur.FG = c
					} else {
						cur.BG = c
					}
					k += 4
				default:
					t.Fatalf("unexpected SGR %q in %q", ps[k], string(rs[i:j+1]))
				}
			}
			i = j
			continue
		}
		for n := runeWidth(rs[i]); n > 0; n-- {
			out = append(out, cur)
		}
	}
	return out
}

func TestSerializedStylesMatchTheCanvas(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	for _, tab := range []int{tabOverview, tabTopology, tabAbout} {
		m.setTab(tab)
		m.Now = m.Now.Add(3 * time.Second)
		m.hits = m.hits[:0]
		cv := NewCanvas(m.Width, m.Height)
		cv.Base = cInk
		m.header(cv)
		m.tabBar(cv, 1)
		m.statusBar(cv, 2)
		m.body(cv, 0, 3, m.Width, m.Height-4)
		m.footer(cv, m.Height-1)
		for y, line := range cv.Lines(colorTrue) {
			got := replay(t, line)
			for x := 0; x < cv.W; x++ {
				p := cv.at(x, y)
				want := p.st
				if want.BG == 0 {
					want.BG = cInk
				}
				g := got[x]
				if g.BG != want.BG || (p.ch != " " && !p.cont && (g.FG != want.FG || g.Attr != want.Attr)) {
					t.Fatalf("tab %d cell %d,%d %q: got %+v want %+v", tab, x, y, p.ch, g, want)
				}
			}
		}
	}
}
