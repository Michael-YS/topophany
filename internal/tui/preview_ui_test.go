package tui

import (
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
