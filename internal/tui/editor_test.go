package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func editorModel(t *testing.T) (*Model, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "topology.json")
	m := &Model{Edit: &Editor{Path: path, Config: testkit.Config()}, Width: 120, Height: 34, NoColor: true, Now: time.Now()}
	m.rebuild()
	return m, path
}
func typeText(m *Model, s string) {
	for _, r := range s {
		m.Key(string(r))
	}
}
func visible(m *Model) string { return ansi.ReplaceAllString(strings.Join(m.Render(), "\n"), "") }

func TestEditorAddNodeWithPlaceLookupAndSave(t *testing.T) {
	m, path := editorModel(t)
	m.Key("2")
	m.Key("a")
	if m.modal == nil || m.modal.kind != "menu" || !strings.Contains(visible(m), "Add to topology") {
		t.Fatal("add menu missing")
	}
	m.Key("n")
	if m.modal == nil || m.modal.form == nil || m.modal.form.kind != "node" {
		t.Fatal("node form missing")
	}
	typeText(m, "tokyo-edge")
	m.Key("tab")
	typeText(m, "Tokyo edge")
	m.Key("field:5")
	typeText(m, "东京")
	m.Key("tab")
	f := m.modal.form
	if f.val("Lat") != "35.68" || f.val("Lon") != "139.69" {
		t.Fatal("gazetteer did not fill coordinates", f.val("Lat"), f.val("Lon"))
	}
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatal("valid form not applied", m.modal.form.err)
	}
	var added *model.Node
	for i, n := range m.Edit.Config.Nodes {
		if n.ID == "tokyo-edge" {
			added = &m.Edit.Config.Nodes[i]
		}
	}
	if added == nil || added.Location == nil || added.Location.Label != "东京" || !m.Edit.Dirty {
		t.Fatal("node not added with location", added)
	}
	if r, ok := m.selectedRow(); !ok || r.ID != "tokyo-edge" || m.Tab != tabTopology {
		t.Fatal("new node not selected", r)
	}
	if _, e := os.Stat(path); e == nil {
		t.Fatal("draft written before explicit save")
	}
	m.Key("ctrl-s")
	c, e := config.Load(path)
	if e != nil || m.Edit.Dirty || !strings.Contains(m.notice, "revision") {
		t.Fatal(e, m.notice)
	}
	if len(c.Nodes) != len(testkit.Config().Nodes)+1 || config.Revision(c) == config.Revision(testkit.Config()) {
		t.Fatal("saved topology mismatch")
	}
}

func TestEditorValidationRefusesBrokenReferencesAndUndo(t *testing.T) {
	m, _ := editorModel(t)
	before := config.Revision(m.Edit.Config)
	m.selectEntity("node", "dmit")
	m.Key("d")
	m.Key("y")
	if !m.noticeErr || !strings.Contains(m.notice, "Cannot delete dmit") || config.Revision(m.Edit.Config) != before {
		t.Fatal("referenced node deleted", m.notice)
	}
	m.selectEntity("check", "home-ssh")
	m.Key("d")
	m.Key("y")
	if m.noticeErr || len(m.Edit.Config.Checks) != len(testkit.Config().Checks)-1 {
		t.Fatal("unreferenced check not deleted", m.notice)
	}
	m.Key("u")
	if config.Revision(m.Edit.Config) != before {
		t.Fatal("undo did not restore")
	}
	// An invalid edit keeps the form open with the validation error.
	m.selectEntity("node", "home")
	m.Key("e")
	f := m.modal.form
	if f.get("ID").readonly != true {
		t.Fatal("identity editable in place")
	}
	m.Key("field:6")
	typeText(m, "91")
	m.Key("field:7")
	typeText(m, "10")
	m.Key("ctrl-s")
	if m.modal == nil || !strings.Contains(m.modal.form.err, "location") {
		t.Fatal("out-of-range latitude accepted")
	}
	m.Key("esc")
	if m.modal != nil || config.Revision(m.Edit.Config) != before {
		t.Fatal("cancel changed draft")
	}
}

func TestEditorFormsPreserveUnlistedFieldsAndQuitGuard(t *testing.T) {
	m, _ := editorModel(t)
	c := m.Edit.Config
	c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:9101"}}
	m.Edit.Config = c
	m.rebuild()
	for _, kind := range []string{"node", "link", "endpoint", "service", "observer", "path", "check"} {
		m.Key("a")
		for i, it := range m.modal.items {
			if it.kind == kind {
				m.Key("menu:" + itoa(i))
			}
		}
		if m.modal == nil || m.modal.form.kind != kind {
			t.Fatal("form", kind)
		}
		m.Key("esc")
	}
	m.selectEntity("path", "direct")
	m.Key("e")
	m.Key("field:1")
	m.Key("end")
	typeText(m, " (edited)")
	m.Key("ctrl-s")
	p := config.Path(m.Edit.Config, "direct")
	if m.modal != nil || p.Name != "DMIT direct (edited)" || len(p.Proxies) != 1 {
		t.Fatal("path edit lost data", p)
	}
	if m.Key("q") != "" || m.modal == nil || m.modal.kind != "confirm" {
		t.Fatal("dirty quit not guarded")
	}
	if m.Key("y") != "quit" {
		t.Fatal("confirmed quit")
	}
}

func TestViewerExplainsReadOnlyEditing(t *testing.T) {
	m := fixtureModel()
	m.Tab = tabServices
	m.Key("e")
	if m.modal == nil || m.modal.kind != "info" || !strings.Contains(visible(&m), "topo edit --config") {
		t.Fatal("read-only hint missing")
	}
	if m.Key("q") != "" || m.modal != nil {
		t.Fatal("q should close the hint, not quit")
	}
	if len(m.Snapshot.Topology.Services) == 0 {
		t.Fatal("fixture")
	}
}

func TestMouseDecodingAndClicks(t *testing.T) {
	var d Decoder
	now := time.Now()
	ks := d.Feed([]byte("\x1b[<0;12;3M\x1b[<65;1;1M\x1b[<0;1234567;3M\x1b[<0;1;1m"), now)
	if strings.Join(ks, ",") != "mouse:0:12:3:M,mouse:65:1:1:M,mouse:0:1:1:m" {
		t.Fatal(ks)
	}
	m := fixtureModel()
	m.Render()
	for _, h := range m.hits {
		if h.key == "tab:3" {
			m.Key("mouse:0:" + itoa(h.x+1) + ":" + itoa(h.y+1) + ":M")
		}
	}
	if m.Tab != 3 {
		t.Fatal("tab click", m.Tab)
	}
	m.Render()
	for _, h := range m.hits {
		if h.key == "row:1" {
			m.Key("mouse:0:" + itoa(h.x+2) + ":" + itoa(h.y+1) + ":M")
		}
	}
	if m.Selected != 1 {
		t.Fatal("row click", m.Selected)
	}
	m.Key("mouse:65:5:5:M")
	if m.Selected != min(4, len(m.Rows())-1) {
		t.Fatal("wheel", m.Selected)
	}
	m.Key("mouse:0:1:1:m")
	m.Key("mouse:2:1:1:M")
	m.Render()
	for _, h := range m.hits {
		if h.key == "key:a" {
			m.Key("mouse:0:" + itoa(h.x+1) + ":" + itoa(h.y+1) + ":M")
		}
	}
	if m.modal == nil {
		t.Fatal("footer Add button not clickable")
	}
}

func TestGeoFitCrossesPacificAndClustersNodes(t *testing.T) {
	pts := [][2]float64{{31.2, 121.5}, {34.05, -118.24}, {35.68, 139.69}}
	g := fitGeo(pts, 160, 100, 0, nil)
	for _, p := range pts {
		x, y := g.project(p[0], p[1])
		if x < 0 || x >= 160 || y < 0 || y >= 100 {
			t.Fatal("point outside view", p, x, y, g)
		}
	}
	if span := 160 * g.scale / g.kx; span > 240 {
		t.Fatal("view goes the long way around", span)
	}
	m := homelabModel(t)
	m.setTab(tabTopology)
	m.MapMode = "geo"
	out := visible(&m)
	// Home Windows, its hosted WSL and the MacBook share Shanghai: one site,
	// one glyph per node.
	if !regexp.MustCompile(`Shanghai [●○▲✖]{3}`).MatchString(out) || !strings.Contains(out, "no location") {
		t.Fatal("cluster/unplaced tray missing", out)
	}
}

func TestAboutAnimatesAndNamesTheEtymology(t *testing.T) {
	m := homelabModel(t)
	m.Key("?")
	if m.Tab != tabAbout {
		t.Fatal("? does not open About")
	}
	a := strings.Join(m.Render(), "\n")
	m.Now = m.Now.Add(400 * time.Millisecond)
	b := strings.Join(m.Render(), "\n")
	if a == b {
		t.Fatal("About art is static")
	}
	m.Now = m.Now.Add(5 * time.Second)
	if out := visible(&m); !strings.Contains(out, "τόπος") || !strings.Contains(out, "the appearance of a place") {
		t.Fatal("etymology missing")
	}
	if m.Animating() > 100*time.Millisecond {
		t.Fatal("About not animated")
	}
}

func TestEveryModeFrameBounds(t *testing.T) {
	ed, _ := editorModel(t)
	models := []func() *Model{
		func() *Model { m := homelabModel(t); return &m },
		func() *Model { m := fixtureModel(); m.NoColor = false; return &m },
		func() *Model { m := *ed; return &m },
	}
	for _, dim := range [][2]int{{30, 10}, {64, 18}, {99, 30}, {100, 28}, {181, 52}} {
		for _, mk := range models {
			for tab := range tabs {
				for _, mode := range []string{"geo", "network"} {
					for zoom := 0; zoom < 3; zoom++ {
						for _, overlay := range []string{"", "a", "e"} {
							m := mk()
							m.Width, m.Height = dim[0], dim[1]
							m.setTab(tab)
							m.MapMode, m.Zoom = mode, zoom
							m.Selected = 2
							if overlay != "" {
								m.Key(overlay)
							}
							lines := m.Render()
							if len(lines) != dim[1] {
								t.Fatal("height", dim, tab, len(lines))
							}
							for _, l := range lines {
								if width(ansi.ReplaceAllString(l, "")) != dim[0] {
									t.Fatalf("width dim=%v tab=%d mode=%s overlay=%q: %q", dim, tab, mode, overlay, ansi.ReplaceAllString(l, ""))
								}
							}
						}
					}
				}
			}
		}
	}
}
