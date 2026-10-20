package tui

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

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

func TestEditorPolicyThresholdIsExplicit(t *testing.T) {
	m, _ := editorModel(t)
	id := m.Edit.Config.Checks[0].ID
	m.openForm("check", id)
	f := m.modal.form
	if f.val("Policy after") != "" {
		t.Fatal("editor opted an existing check into delayed policy alerts")
	}
	f.get("Policy after").value = "3"
	if err := f.commit(&m.Edit.Config); err != nil {
		t.Fatal(err)
	}
	if m.Edit.Config.Checks[0].PolicyThreshold != 3 || config.Validate(m.Edit.Config) != nil {
		t.Fatal("policy confirmation was not saved")
	}
	m.openForm("check", id)
	f = m.modal.form
	f.get("Policy after").value = ""
	if err := f.commit(&m.Edit.Config); err != nil || m.Edit.Config.Checks[0].PolicyThreshold != 0 {
		t.Fatal("editor could not restore immediate policy alerts", err)
	}
}

func TestEditorTraceFieldsRoundTrip(t *testing.T) {
	m, _ := editorModel(t)
	id := m.Edit.Config.Checks[0].ID
	m.Edit.Config.Checks[0].HTTP.Contains = "READY"
	m.openForm("check", id)
	f := m.modal.form
	if f.get("Exit format").kind != fChoice || f.get("Exit JSON key").kind != fText || f.get("Trace warp").kind != fChoice || f.get("Trace gateway").kind != fChoice || f.get("Trace colos").kind != fList {
		t.Fatal("trace fields are not choices, a key and a list")
	}
	for label, want := range map[string]string{
		"Exit format":   ",plain,trace,json",
		"Trace warp":    ",off,on,plus",
		"Trace gateway": ",on,off",
	} {
		if got := strings.Join(f.get(label).choices, ","); got != want {
			t.Fatalf("%s choices are %q; want %q", label, got, want)
		}
	}
	f.get("Exit format").value = "trace"
	f.get("Exit JSON key").value = "address"
	f.get("Trace warp").value = "on"
	f.get("Trace gateway").value = "off"
	f.get("Trace colos").value = "HKG, NRT"
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatal("valid trace form not applied", m.modal.form.err)
	}
	x := m.Edit.Config.Checks[0]
	if x.HTTP.ExitFormat != "trace" || x.HTTP.ExitJSONKey != "address" || x.HTTP.ExpectedTrace == nil || x.HTTP.ExpectedTrace.Warp != "on" || x.HTTP.ExpectedTrace.Gateway != "off" || len(x.HTTP.ExpectedTrace.Colos) != 2 || x.HTTP.Contains != "READY" {
		t.Fatalf("trace fields not saved or unlisted field lost: %+v", x.HTTP)
	}
	m.openForm("check", id)
	f = m.modal.form
	if f.val("Exit format") != "trace" || f.val("Exit JSON key") != "address" || f.val("Trace warp") != "on" || f.val("Trace gateway") != "off" || f.val("Trace colos") != "HKG, NRT" {
		t.Fatal("trace fields did not round-trip", f.val("Exit format"), f.val("Exit JSON key"), f.val("Trace warp"), f.val("Trace gateway"), f.val("Trace colos"))
	}
	// Clearing every trace field removes the expectation; the exit format is
	// its own option and stays as chosen.
	f.get("Trace warp").value = ""
	f.get("Trace gateway").value = ""
	f.get("Trace colos").value = ""
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatal("cleared trace fields not applied", m.modal.form.err)
	}
	if x := m.Edit.Config.Checks[0]; x.HTTP.ExpectedTrace != nil || x.HTTP.ExitFormat != "trace" || x.HTTP.ExitJSONKey != "address" {
		t.Fatalf("empty trace fields kept an expectation or lost the exit options: %+v", x.HTTP)
	}
	// A trace field without the format that parses a trace is refused by the
	// same validation the save runs, and the draft is left alone.
	m.openForm("check", id)
	f = m.modal.form
	f.get("Exit format").value = ""
	f.get("Trace warp").value = "on"
	m.Key("ctrl-s")
	if m.modal == nil || !strings.Contains(m.modal.form.err, "expected_trace requires exit_format trace") {
		t.Fatal("trace expectation without exit_format trace was accepted")
	}
	if x := m.Edit.Config.Checks[0]; x.HTTP.ExpectedTrace != nil || x.HTTP.ExitFormat != "trace" {
		t.Fatalf("refused form changed the draft: %+v", x.HTTP)
	}
}

func TestEditorSecretRequestHeadersRoundTrip(t *testing.T) {
	m, _ := editorModel(t)
	id := m.Edit.Config.Checks[0].ID
	m.Edit.Config.Checks[0].HTTP.Contains = "READY"
	m.Edit.Config.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Literal": "kept"}
	m.openForm("check", id)
	f := m.modal.form
	if f.get("Secret request headers").kind != fList {
		t.Fatal("secret request headers are not a list")
	}
	f.get("Secret request headers").value = "CF-Access-Client-Secret=CF_ACCESS_SECRET, CF-Access-Client-Id=CF_ACCESS_ID"
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatal("valid secret request headers not applied", m.modal.form.err)
	}
	x := m.Edit.Config.Checks[0]
	if x.HTTP.Contains != "READY" || x.HTTP.RequestHeaders["X-Literal"] != "kept" || x.HTTP.RequestHeaderEnvs["CF-Access-Client-Id"] != "CF_ACCESS_ID" || x.HTTP.RequestHeaderEnvs["CF-Access-Client-Secret"] != "CF_ACCESS_SECRET" || len(x.HTTP.RequestHeaderEnvs) != 2 {
		t.Fatalf("secret request headers not saved or unlisted field lost: %+v", x.HTTP)
	}
	m.openForm("check", id)
	f = m.modal.form
	if got := f.val("Secret request headers"); got != "CF-Access-Client-Id=CF_ACCESS_ID, CF-Access-Client-Secret=CF_ACCESS_SECRET" {
		t.Fatalf("secret request headers did not round-trip: %q", got)
	}
	// Clearing the field removes the whole map rather than writing an empty
	// one; an empty topology field is the same execution as no field.
	f.get("Secret request headers").value = ""
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatal("cleared secret request headers not applied", m.modal.form.err)
	}
	if x := m.Edit.Config.Checks[0]; x.HTTP.RequestHeaderEnvs != nil {
		t.Fatalf("empty field kept a map: %#v", x.HTTP.RequestHeaderEnvs)
	}
	// A missing '=' is refused while the draft stays as it was.
	m.openForm("check", id)
	f = m.modal.form
	f.get("Secret request headers").value = "CF-Access-Client-Id"
	m.Key("ctrl-s")
	if m.modal == nil || !strings.Contains(m.modal.form.err, "Name=ENV_VAR") {
		t.Fatal("entry without '=' accepted", m.modal)
	}
	if x := m.Edit.Config.Checks[0]; x.HTTP.RequestHeaderEnvs != nil {
		t.Fatalf("refused entry changed the draft: %#v", x.HTTP.RequestHeaderEnvs)
	}
	// An entry that parses here but names an invalid header is refused by the
	// same whole-topology validation the save runs.
	m.Key("esc")
	m.openForm("check", id)
	f = m.modal.form
	f.get("Secret request headers").value = "Bad Header=CF_ACCESS_ID"
	m.Key("ctrl-s")
	if m.modal == nil || !strings.Contains(m.modal.form.err, "invalid HTTP header name") {
		t.Fatal("invalid header name accepted", m.modal)
	}
	// Header names are unique whatever their case.
	m.Key("esc")
	m.openForm("check", id)
	f = m.modal.form
	f.get("Secret request headers").value = "X-Key=ENV_A, x-key=ENV_B"
	m.Key("ctrl-s")
	if m.modal == nil || !strings.Contains(m.modal.form.err, "duplicate secret request header name") {
		t.Fatal("duplicate secret request header accepted", m.modal)
	}
	if x := m.Edit.Config.Checks[0]; x.HTTP.RequestHeaderEnvs != nil {
		t.Fatalf("refused duplicate changed the draft: %#v", x.HTTP.RequestHeaderEnvs)
	}
}

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

// On cells 2.6 times as tall as wide the geographic map keeps its shape on
// screen: the same latitudes over fewer rows, every place still in view and
// still where a click finds it.
func TestGeoKeepsItsShapeOnTallCells(t *testing.T) {
	m := homelabModel(t)
	m.setTab(tabTopology)
	m.MapMode = "geo"
	square := m.geoRows(120)
	m.Cell = 2.6
	if tall, want := m.geoRows(120), float64(square)*2/2.6; float64(tall) < want-1 || float64(tall) > want+1 {
		t.Fatalf("map is %d rows on 2.6:1 cells and %d on 2:1", tall, square)
	}
	pts := [][2]float64{{31.2, 121.5}, {34.05, -118.24}, {35.68, 139.69}}
	g := fitGeo(pts, 160, 100, 1.3, 0, nil)
	for _, p := range pts {
		x, y := g.project(p[0], p[1])
		if x < 0 || x >= 160 || y < 0 || y >= 100 {
			t.Fatal("point outside view", p, x, y, g)
		}
		if lat, lon, ok := g.at(x, y); !ok || math.Abs(lat-p[0]) > 1e-9 || math.Abs(math.Mod(lon-p[1]+540, 360)-180) > 1e-9 {
			t.Fatal("at does not invert project", p, lat, lon)
		}
	}
	if out := mapText(&m, 84); !regexp.MustCompile(`▎ Shanghai`).MatchString(out) || !regexp.MustCompile(`▎ [●○▲✖] Home Windows`).MatchString(out) {
		t.Fatal("cluster missing on tall cells", out)
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
	g := fitGeo(pts, 160, 100, 1, 0, nil)
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
	// labelled by a plate with a row for each node, WSL under its host.
	for _, want := range []string{`▎ Shanghai`, `▎ [●○▲✖] Home Windows`, `▎ └ [●○▲✖] WSL mirrored`, `▎ [●○▲✖] MacBook`, `no location`} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Fatal("cluster/unplaced tray missing", want, out)
		}
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
	// The wheel turns the globe instead of moving a selection.
	still := strings.Join(m.Render(), "\n")
	m.Key("mouse:65:20:10:M")
	if m.aboutSpin == 0 || m.Selected != 0 || strings.Join(m.Render(), "\n") == still {
		t.Fatal("wheel does not turn the globe")
	}
}

// About says where Topophany lives, as text anyone can copy and, where the
// terminal takes them, as hyperlinks; small screens close up to keep them,
// and big ones add the keys.
func TestAboutLinksTheHomepageAndTheSource(t *testing.T) {
	m := homelabModel(t)
	m.setTab(tabAbout)
	m.Now = m.Now.Add(5 * time.Second)
	for _, dim := range [][2]int{{80, 24}, {120, 36}, {211, 43}} {
		m.Width, m.Height = dim[0], dim[1]
		out := visible(&m)
		if !strings.Contains(out, "topophany.com") || !strings.Contains(out, "github.com/WhitenWhiten/topophany") {
			t.Fatalf("%v: About lacks the links\n%s", dim, out)
		}
		if strings.Contains(strings.Join(m.Render(), ""), "\x1b]8;") {
			t.Fatal("hyperlinks written to a terminal that did not ask for them")
		}
	}
	// With room to spare, About also lists the keys.
	if out := visible(&m); !strings.Contains(out, " evidence") || !strings.Contains(out, " quit") {
		t.Fatal("About lacks the keys", out)
	}
	m.Hyperlinks = true
	raw := ansi.ReplaceAllString(strings.Join(m.Render(), "\n"), "")
	for _, want := range []string{"\x1b]8;;" + homepage + "\x1b\\topophany.com\x1b]8;;\x1b\\", "\x1b]8;;" + repo + "\x1b\\github.com/WhitenWhiten/topophany\x1b]8;;\x1b\\"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("About lacks the hyperlink %q", want)
		}
	}
}

// The globe is a lit ball, not a disc: shaded under its dots, brighter
// toward the light than across the terminator, and as tall as it is wide.
func TestGlobeIsShadedAsASphere(t *testing.T) {
	m := homelabModel(t)
	d := NewDots(60, 30)
	m.paintGlobe(d, 120, 120, 1, 0, 0) // R = 54 dots about (60, 60)
	if d.shade == nil {
		t.Fatal("globe is flat: nothing is shaded under its dots")
	}
	at := func(px, py int) tint { return d.shade[(py/4)*d.W+px/2] }
	luma := func(px, py int) float64 { s := at(px, py); return (s.r + s.g + s.b) / s.a }
	if lit, dark := luma(60-27, 60-22), luma(60+32, 60+27); lit < dark*1.8 {
		t.Fatalf("sunward %.0f is not brighter than the night side %.0f", lit, dark)
	}
	if at(2, 2).a != 0 || at(60, 60).a != 8 {
		t.Fatal("shading leaks off the ball or leaves its centre bare")
	}
	extent := func(shaded func(i int) bool, n, dots int) int {
		k := 0
		for i := 0; i < n; i++ {
			if shaded(i) {
				k++
			}
		}
		return k * dots
	}
	wide := extent(func(x int) bool { return d.shade[15*d.W+x].a >= 4 }, d.W, 2)
	tall := extent(func(y int) bool { return d.shade[y*d.W+30].a >= 4 }, d.H, 4)
	if wide < 100 || tall < 100 || wide-tall > 4 || tall-wide > 4 {
		t.Fatalf("ball is %d dots wide and %d tall", wide, tall)
	}
	// The xterm cube would band the shading, so 256 colours keep the dots.
	m.Colors = color256
	cv := NewCanvas(60, 30)
	m.drawGlobe(cv, 0, 0, 60, 30, 0, 0)
	for _, c := range cv.cells {
		if c.st.BG != 0 {
			t.Fatal("256-colour globe is shaded")
		}
	}
}

// On cells taller than 2:1 a dot is taller than it is wide, so the globe
// spans fewer rows of dots than columns and is round on screen.
func TestGlobeIsRoundOnTallCells(t *testing.T) {
	m := homelabModel(t)
	const ky = 1.3 // cells 2.6 times as tall as wide
	d := NewDots(60, 30)
	m.paintGlobe(d, 120, 120, ky, 0, 0)
	wide, tall := 0, 0
	for x := 0; x < d.W; x++ {
		if d.shade[15*d.W+x].a >= 4 {
			wide += 2
		}
	}
	for y := 0; y < d.H; y++ {
		if d.shade[y*d.W+30].a >= 4 {
			tall += 4
		}
	}
	if h := float64(tall) * ky; wide < 100 || h < float64(wide)-6 || h > float64(wide)+6 {
		t.Fatalf("ball is %d dot widths wide and %.0f tall", wide, h)
	}
	// The About view gives the globe columns for its rows to match: on a
	// 160x40 screen the shaded ball, left of the text, is as many cell
	// widths wide as its rows are tall, and as tall as the view allows.
	m.Cell = 2.6
	m.Width, m.Height = 160, 40
	m.setTab(tabAbout)
	cv := m.draw()
	rows, cols := 0, 0
	for y := 3; y < cv.H-2; y++ {
		n := 0
		for x := 0; x < 100; x++ {
			if bg := cv.cells[y*cv.W+x].st.BG; bg != 0 && bg != cInk {
				n++
			}
		}
		if n > 0 {
			rows++
		}
		cols = max(cols, n)
	}
	if h := float64(rows) * m.Cell; rows < 30 || float64(cols) < h-6 || float64(cols) > h+6 {
		t.Fatalf("globe on 2.6:1 cells is %d columns by %d rows", cols, rows)
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
				for _, mode := range []string{"virtual", "geo", "network"} {
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

// A field shows no terminal control or bidi rune a topology puts in it,
// at the cursor too, in color or not, and its cursor stands where the
// text it is on is shown.
func TestEditorFieldsShowNoControls(t *testing.T) {
	var b strings.Builder
	for r := rune(0); r < 0xa0; r++ {
		if unicode.IsControl(r) {
			b.WriteString("x" + string(r))
		}
	}
	for _, r := range "؜‎‏‪‫‬‭‮⁦⁧⁨⁩" {
		b.WriteString("x" + string(r))
	}
	edit := func(t *testing.T, name string, noColor bool) *Model {
		m, _ := editorModel(t)
		m.NoColor = noColor
		c := m.Edit.Config
		c.Nodes[0].Name = name
		m.Edit.Config = c
		m.rebuild()
		m.selectEntity("node", c.Nodes[0].ID)
		m.Key("e")
		m.Key("field:1")
		m.Key("home")
		if m.modal == nil || m.modal.form.val("Name") != name {
			t.Fatal("name field not focused")
		}
		return m
	}
	for _, noColor := range []bool{false, true} {
		m := edit(t, b.String(), noColor)
		for i := range len([]rune(b.String())) + 1 {
			for _, l := range m.Render() {
				for _, r := range ansi.ReplaceAllString(l, "") {
					if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
						t.Fatalf("cursor at %d (no color %v) shows %U", i, noColor, r)
					}
				}
			}
			m.Key("right")
		}
	}
	m := edit(t, "ab\x1b‮cd", false)
	for range 4 {
		m.Key("right")
	}
	cv := m.draw()
	found := false
	for y := range cv.H {
		for x := 1; x < cv.W; x++ {
			if cv.at(x, y).st == (Style{FG: cDeep, BG: cAccent}) {
				found = true
				if cv.at(x, y).ch != "c" || cv.at(x-1, y).ch != "b" {
					t.Fatalf("cursor on %q after %q", cv.at(x, y).ch, cv.at(x-1, y).ch)
				}
			}
		}
	}
	if !found {
		t.Fatal("no cursor")
	}
}

func TestEditorSavePreservesAddressReferences(t *testing.T) {
	m, path := editorModel(t)
	m.Edit.Config.Addresses = map[string]string{"entry": "192.0.2.10"}
	m.Edit.Config.Endpoints[0].Address = "https://${entry}/"
	if _, e := m.Edit.Save(); e != nil {
		t.Fatal(e)
	}
	c, e := config.Load(path)
	if e != nil || c.Endpoints[0].Address != "https://${entry}/" || c.Addresses["entry"] != "192.0.2.10" {
		t.Fatal(e, c.Endpoints[0])
	}
}
