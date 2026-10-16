package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// previewLang is the language the preview is read in here: one whose names
// are wide characters, which the layout must measure by their cells.
const previewLang = "zh"

func previewModel(t *testing.T) *Model {
	t.Helper()
	c, e := preview.Preset(previewLang)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sim := preview.New(c, now, previewLang)
	snap, e := sim.Snapshot("", now)
	if e != nil {
		t.Fatal(e)
	}
	return &Model{Snapshot: snap, Now: now, Width: 160, Height: 48, Preview: sim, Server: "synthetic"}
}

// selectFirstEntry selects the first row of the list that is not a group,
// as the preview's Services and Checks open grouped, and returns it.
func selectFirstEntry(t *testing.T, m *Model) Row {
	t.Helper()
	for i, r := range m.Rows() {
		if r.Kind != "group" {
			m.Selected = i
			return r
		}
	}
	t.Fatal("the list has only groups")
	return Row{}
}

func TestPreviewScenarioMenuDebugAndReadOnly(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabTopology)
	out := visible(m)
	if !strings.Contains(out, "PREVIEW") || !strings.Contains(out, "全部正常") || !strings.Contains(out, "成都") {
		t.Fatal("preview chrome or geography missing")
	}
	if !strings.Contains(out, "Scenario") || !strings.Contains(out, "Settings") {
		t.Fatal("preview footer dropped a key", out[len(out)-400:])
	}
	m.Key("x")
	if m.modal == nil || m.modal.kind != "menu" || len(m.modal.items) < 8 || !strings.Contains(m.modal.items[1].hint, "回落") {
		t.Fatal("scenario menu", m.modal)
	}
	if m.Key("2") != "refresh" || !strings.Contains(m.notice, "中转") {
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
		for _, mode := range []string{"virtual", "geo", "network"} {
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
	c, e := preview.Preset(previewLang)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sim := preview.New(c, now, previewLang)
	snap, _ := sim.Snapshot("", now)
	m := &Model{Snapshot: snap, Now: now, Width: 160, Height: 48, Preview: sim, Server: "synthetic"}
	out := visible(m)
	for _, want := range []string{"ALL CLEAR", "checks passing", "Nothing needs attention", "SERVICES", "OBSERVERS", "成都"} {
		if !strings.Contains(out, want) {
			t.Fatalf("healthy overview lacks %q", want)
		}
	}
	// Transit blocked from mainland observers: the verdict, the worst-first
	// attention list and the route of the selected incident.
	sim.SetScenario(1, now.Add(-30*time.Minute))
	m.Snapshot, _ = sim.Snapshot("", now)
	out = visible(m)
	if !strings.Contains(out, "INCIDENTS") || !strings.Contains(out, "checks failing") || !strings.Contains(out, "主链搬瓦工 → Oracle 出口 A") {
		t.Fatal("blocked overview lacks verdict or incident", out)
	}
	rows := m.Rows()
	if len(rows) == 0 || rows[0].Kind != "incident" {
		t.Fatal("attention list does not lead with incidents", rows)
	}
	for i := 1; i < len(rows); i++ {
		if view.AttentionRank(rows[i]) < view.AttentionRank(rows[i-1]) {
			t.Fatal("attention list not worst first", rows[i-1].Title, rows[i].Title)
		}
	}
	f := m.focus(rows[0])
	if !f.nodes["transit"] || !f.nodes["exit-a"] || !f.edges["transit-exit-a"] {
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
		m.header(cv, 0, 0, m.Width)
		m.tabBar(cv, 0, 1, m.Width)
		m.statusBar(cv, 0, 2, m.Width)
		m.body(cv, 0, 3, m.Width, m.Height-4)
		m.footer(cv, 0, m.Height-1, m.Width)
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

// Services deployed on or reached through an edge say so where they are
// listed, a check answered by an edge says which and from where, and an
// endpoint behind an edge implies a hop on the maps when no link does.
func TestEdgesShowWhereServicesRunAndWhoAnswered(t *testing.T) {
	m := previewModel(t)
	cols := func(tab int, id string) []string {
		m.setTab(tab)
		for _, r := range m.Rows() {
			if r.ID == id {
				return r.Cols
			}
		}
		t.Fatal("missing row", id)
		return nil
	}
	if c := cols(tabServices, "blog"); !strings.HasPrefix(c[0], "Cloudflare ⇢ Oracle 纽约") {
		t.Fatal("the way into the blog does not start at the edge", c)
	}
	if c := cols(tabServices, "shortlink"); c[0] != "Cloudflare · workers" {
		t.Fatal("a Worker should say where and how it runs", c)
	}
	if c := cols(tabServices, "webapp"); c[0] != "Vercel · functions" {
		t.Fatal(c)
	}
	if c := cols(tabTopology, "app-web"); c[0] != "public · via cf" {
		t.Fatal("endpoint behind the edge", c)
	}
	if c := cols(tabTopology, "cf"); c[1] != "edge · Cloudflare" {
		t.Fatal("edge node kind and provider", c)
	}
	m.setTab(tabChecks)
	for _, r := range m.Rows() {
		if r.ID == "j-blog" && !strings.Contains(strings.Join(r.Details, "\n"), "Answered by: Cloudflare FRA · cache DYNAMIC") {
			t.Fatal("check evidence does not name the edge that answered", r.Details)
		}
	}

	links := m.Snapshot.Topology.Links[:0]
	for _, l := range m.Snapshot.Topology.Links {
		if l.ID != "cf-app" {
			links = append(links, l)
		}
	}
	m.Snapshot.Topology.Links = links
	_, edges, _ := m.graph()
	pairs := 0
	for _, e := range edges {
		if e.From == "cf" && e.To == "app" {
			pairs++
			if e.Kind != edgeVia || e.Signal != "carried" {
				t.Fatal("implied hop behind the edge", e)
			}
		}
	}
	if pairs != 1 {
		t.Fatal("the hop behind the edge is drawn", pairs, "times")
	}
}

// The Servers tab is a card per server: containers sit on their host's
// card and external parties have none. A card carries the services and the
// edge in front of them, what it hosts and the notes as a memo, and leads
// to what it lists; edge cards say what runs there and where they answered.
func TestServersTabCardsAndMemo(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	m.Key("3")
	if m.Tab != tabServers {
		t.Fatal("3 does not open Servers", m.Tab)
	}
	ids := []string{}
	for _, r := range m.Rows() {
		if r.Kind != "node" {
			t.Fatal("a card that is not a node", r)
		}
		ids = append(ids, r.ID)
	}
	for _, id := range []string{"win", "app", "git-node", "cf", "vercel", "supabase"} {
		if !contains(ids, id) {
			t.Fatal("no card for", id, ids)
		}
	}
	for _, id := range []string{"internet", "visitors", "app-db", "jp-openlist"} {
		if contains(ids, id) {
			t.Fatal("card for an external party or a container", id)
		}
	}
	m.Key("select:node:app")
	out := visible(m)
	for _, want := range []string{"Oracle 纽约", "SERVICES", "via Cloudflare", "not monitored", "HOSTS", "PostgreSQL 容器", "✎ 主要建站与核心应用服务器"} {
		if !strings.Contains(out, want) {
			t.Fatalf("app's card lacks %q", want)
		}
	}
	clicked := false
	for _, h := range m.hits {
		if h.key == "goto:service:blog" {
			m.Key("mouse:0:" + itoa(h.x+2) + ":" + itoa(h.y+1) + ":M")
			clicked = true
			break
		}
	}
	if r, ok := m.selectedRow(); !clicked || m.Tab != tabServices || !ok || r.ID != "blog" {
		t.Fatal("a service on a card does not open it", clicked, m.Tab)
	}
	m.setTab(tabServers)
	m.Key("select:node:cf")
	out = visible(m)
	for _, want := range []string{"Cloudflare", "edge · Cloudflare", "RUNS HERE", "workers", "IN FRONT OF", "www.example.invalid", "ANSWERED FROM"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the edge's card lacks %q", want)
		}
	}
	if got := memoLines("line one\nline two that wraps", 12); len(got) != 3 || got[0] != "line one" {
		t.Fatal("memo line breaks", got)
	}
	m.Search = "备份仓库"
	if rows := m.Rows(); len(rows) != 1 || rows[0].ID != "git-node" {
		t.Fatal("memos are not searchable", rows)
	}
}

// Tabs count what in them needs a look, and only from current evidence;
// with no room to count, the number of a tab that needs a look is tinted.
func TestTabsCountWhatNeedsALook(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	for tab := range tabs {
		if n, _ := m.badge(tab); n != 0 {
			t.Fatalf("healthy preview: tab %s counts %d", tabs[tab], n)
		}
	}
	sim := m.Preview.(*preview.Sim)
	sim.SetScenario(1, m.Now.Add(-30*time.Minute))
	m.Snapshot, _ = sim.Snapshot("", m.Now)
	if n, c := m.badge(tabIncidents); n != len(m.Snapshot.Incidents) || n == 0 || c != cDown {
		t.Fatalf("incidents badge %d %v, want %d red", n, c, len(m.Snapshot.Incidents))
	}
	failing := 0
	for _, c := range m.Snapshot.Checks {
		if c.Health == "down" || c.Health == "degraded" {
			failing++
		}
	}
	if n, _ := m.badge(tabChecks); n != failing {
		t.Fatalf("checks badge %d, want %d", n, failing)
	}
	m.Width = 211
	strip := ansi.ReplaceAllString(m.Render()[1], "")
	if !strings.Contains(strip, "Incidents "+itoa(len(m.Snapshot.Incidents))+" ") || !strings.Contains(strip, "Checks "+itoa(failing)+" ") {
		t.Fatalf("tab strip lacks its counts: %q", strip)
	}
	// At 80 columns the names collapse and the numbers carry the color.
	m.Width = 80
	cv := m.draw()
	tinted := false
	for x := 0; x < cv.W; x++ {
		if p := cv.at(x, 1); p.ch == "7" && p.st.FG == cDown {
			tinted = true
		}
	}
	if !tinted {
		t.Fatal("collapsed Incidents tab is not tinted")
	}
	// A cached view claims nothing.
	m.Error = "collector unavailable"
	if n, _ := m.badge(tabIncidents); n != 0 {
		t.Fatal("cached view counts incidents")
	}
}

// The window is named after the verdict, and nothing from the collector can
// smuggle a control sequence into the title.
func TestWindowTitleIsTheVerdict(t *testing.T) {
	m := previewModel(t)
	if got := m.title(); got != "● all clear · preview-homelab · Topophany" {
		t.Fatalf("healthy title %q", got)
	}
	sim := m.Preview.(*preview.Sim)
	sim.SetScenario(1, m.Now.Add(-30*time.Minute))
	m.Snapshot, _ = sim.Snapshot("", m.Now)
	if got := m.title(); !strings.HasPrefix(got, "✖ "+itoa(len(m.Snapshot.Incidents))+" incidents · ") {
		t.Fatalf("outage title %q", got)
	}
	m.Loading = "loading all observers"
	if got := m.title(); !strings.HasPrefix(got, "◌ loading · ") {
		t.Fatalf("loading title %q", got)
	}
	m.Loading = ""
	m.Snapshot.Project = "lab\x1b]2;owned\x07\u009d"
	if got := m.title(); strings.ContainsAny(got, "\x1b\x07\u009d") {
		t.Fatalf("title carries control characters: %q", got)
	}
	ed := &Model{Edit: &Editor{Path: "/srv/topo/topology.json", Dirty: true}}
	if got := ed.title(); got != "✎ topology.json ● · Topophany" {
		t.Fatalf("editor title %q", got)
	}
}

// At 80x24 the Overview still gives the verdict its big number and puts the
// rest of the room to use: observers beside it, then the services by
// application when nothing needs attention, or the attention list when
// something does.
func TestSmallOverviewUsesItsRoom(t *testing.T) {
	m := previewModel(t)
	m.Width, m.Height = 80, 24
	out := visible(m)
	for _, want := range []string{"ALL CLEAR", "checks passing", "OBSERVERS", "成都 Windows", "Nothing needs attention", "SERVICES · 23 in 6 applications", view.ApplicationName(m.Snapshot.Topology, "main-site")} {
		if !strings.Contains(out, want) {
			t.Fatalf("healthy 80x24 overview lacks %q\n%s", want, out)
		}
	}
	sim := m.Preview.(*preview.Sim)
	sim.SetScenario(1, m.Now.Add(-30*time.Minute))
	m.Snapshot, _ = sim.Snapshot("", m.Now)
	out = visible(m)
	for _, want := range []string{"checks failing", "NEEDS ATTENTION", "主链搬瓦工 → Oracle 出口 A"} {
		if !strings.Contains(out, want) {
			t.Fatalf("80x24 overview of an outage lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "SERVICES") {
		t.Fatal("an outage's attention list gives its room to the services")
	}
}

// A server card names its server once, its state in the pill; its sections
// open with a quiet label and a hairline; a server with a problem takes the
// problem's color across its header, so it stands out on the wall; and the
// card's edge is its frame, with nothing painted outside it.
func TestServerCardsPutTroubleUpFront(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	m.Width, m.Height = 160, 45
	sim := m.Preview.(*preview.Sim)
	sim.SetScenario(slices.Index(sim.ScenarioIDs(), "transit-blocked"), m.Now.Add(-30*time.Minute))
	snap, e := sim.Snapshot("", m.Now)
	if e != nil {
		t.Fatal(e)
	}
	m.Snapshot = snap
	m.setTab(tabServers)
	m.Key("select:node:transit")
	out := visible(m)
	if !regexp.MustCompile(`│▎搬瓦工 洛杉矶 +● AFFECTED +│`).MatchString(out) {
		t.Fatal("the header should name the server once, its state in the pill", out)
	}
	if !regexp.MustCompile(`SERVICES ─{10,}`).MatchString(out) || !regexp.MustCompile(`ADDRESSES 2 ─{10,}`).MatchString(out) {
		t.Fatal("sections do not open with a label and a hairline", out)
	}
	cv := m.draw()
	for y := 0; y < cv.H; y++ {
		for x := 1; x+4 < cv.W; x++ {
			if cv.at(x-1, y).ch != "▎" || cv.at(x, y).ch != "搬" {
				continue
			}
			if bg := cv.at(x, y).st.BG; bg != Mix(Mix(cWarn, cInk, .86), cSelBG, .35) {
				t.Fatalf("an affected server's header is not amber: %06x", uint32(bg)&0xFFFFFF)
			}
			if frame := cv.at(x-2, y-1); frame.ch != "┏" && frame.ch != "╭" || frame.st.BG != 0 && frame.st.BG != cInk {
				t.Fatalf("the frame is painted outside its line: %q %06x", frame.ch, uint32(frame.st.BG)&0xFFFFFF)
			}
			return
		}
	}
	t.Fatal("no transit card drawn")
}
