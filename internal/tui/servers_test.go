package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
)

// cardText is what a card's rows say, a line a row.
func cardText(rows []cardRow) string {
	var b strings.Builder
	for _, r := range rows {
		for _, sp := range append(r.left, r.right...) {
			b.WriteString(sp.s)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// cardHeights are the heights of the card frames drawn inside the panel,
// by their top-left corners.
func cardHeights(cv *Canvas) []int {
	out := []int{}
	for y := 0; y < cv.H; y++ {
		for x := 1; x < cv.W; x++ {
			if c := cv.at(x, y).ch; c != "╭" && c != "┏" {
				continue
			}
			for yy := y + 1; yy < cv.H; yy++ {
				if c := cv.at(x, yy).ch; c == "╰" || c == "┗" {
					out = append(out, yy-y+1)
					break
				}
			}
		}
	}
	return out
}

// s lays the Servers cards out as a grid of one size, each saying the same
// things in the same places, and s again brings the wall back; elsewhere s
// does nothing.
func TestServersUniformGrid(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	m.Height = 60
	m.Key("2")
	m.Key("s")
	if m.Uniform {
		t.Fatal("s lays out the cards from another tab")
	}
	m.Key("3")
	if out := visible(m); !strings.Contains(out, "Uniform") || strings.Contains(out, "SERVERS · uniform") {
		t.Fatal("the wall does not offer the grid", out)
	}
	wall := cardHeights(m.draw())
	m.Key("s")
	if !m.Uniform {
		t.Fatal("s does not lay out a grid")
	}
	out := visible(m)
	if !strings.Contains(out, "SERVERS · uniform") || !strings.Contains(out, "Full cards") {
		t.Fatal("the grid does not say so, or offer the wall back", out)
	}
	heights := cardHeights(m.draw())
	if len(heights) < 6 || slices.Min(heights) != tileRows+2 || slices.Max(heights) != tileRows+2 {
		t.Fatal("uniform cards differ in height", heights)
	}
	if slices.Min(wall) == slices.Max(wall) {
		t.Fatal("the wall's cards are all one height", wall)
	}

	f := m.fleet()
	row := func(id string) Row {
		i := slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == id })
		if i < 0 {
			t.Fatal("no card for", id)
		}
		return m.Rows()[i]
	}
	// A host: its resources, what runs there, how it is reached and what
	// measures it, its address and memo; every panel row the card's width.
	app := m.tile(f, row("app"), 46, false)
	if len(app) != tileRows {
		t.Fatal("a uniform card has", len(app), "rows")
	}
	text := cardText(app)
	for _, want := range []string{"CPU", "MEM", "DISK", "8 CPUs", "SERVICES", "ADDRESSES", "CHECKS", "1 unchecked", "https://www.example.invalid/", "+4", "✎ 主要建站与核心应用服务器"} {
		if !strings.Contains(text, want) {
			t.Fatalf("app's uniform card lacks %q:\n%s", want, text)
		}
	}
	for _, r := range append(app[3:7:7], app[8:11]...) {
		w := 0
		for _, sp := range r.left {
			w += width(sp.s)
		}
		if w != 46 {
			t.Fatalf("a panel row is %d wide, not 46: %q", w, cardText([]cardRow{r}))
		}
	}
	// An edge has no machine of yours to read: where a host's resources
	// stand, what it is in front of and where it answered from; with
	// nothing in front of it, who hosts it.
	for id, want := range map[string][]string{
		"cf":       {"IN FRONT OF", "www.example.invalid", "→ Oracle", "answered from LAX 49% · FRA 40% · EWR 10%", "RUNS HERE"},
		"vercel":   {"hosted by Vercel", "answered from edge region fra1"},
		"supabase": {"hosted by Supabase", "no machine of yours to measure"},
	} {
		edge := m.tile(f, row(id), 46, false)
		text := cardText(edge)
		if len(edge) != tileRows || strings.Contains(text, "CPU") || strings.Contains(text, "MEM") || strings.Contains(text, "DISK") {
			t.Fatalf("%s's uniform card draws resources:\n%s", id, text)
		}
		for _, w := range want {
			if !strings.Contains(text, w) {
				t.Fatalf("%s's uniform card lacks %q:\n%s", id, w, text)
			}
		}
	}
	// A host nothing reads says why in their place, rather than charting
	// nothing: its worker runs no host check, or no worker runs there.
	topo := &m.Snapshot.Topology
	topo.Checks = slices.DeleteFunc(slices.Clone(topo.Checks), func(x model.Check) bool { return x.ID == "host-git-node" })
	for i, want := range []string{"its worker runs no host check", "no worker runs here"} {
		if i == 1 {
			topo.Observers = slices.Clone(topo.Observers)
			for k := range topo.Observers {
				if topo.Observers[k].Node == "git-node" {
					topo.Observers[k].Node = ""
				}
			}
		}
		git := m.tile(m.fleet(), row("git-node"), 46, false)
		text := cardText(git)
		if len(git) != tileRows || strings.Contains(text, "—") || !strings.Contains(text, want) || !strings.Contains(text, "CPU, memory and disk unmeasured") {
			t.Fatalf("an unread host's uniform card, want %q:\n%s", want, text)
		}
	}

	many := make([]mark, 12)
	for i := range many {
		many[i] = mark{'●', cUp}
	}
	got := markSpans(many, 9)
	if text := cardText([]cardRow{{left: got}}); width(strings.TrimSuffix(text, "\n")) != 9 || !strings.HasPrefix(text, "●●●●●●+6") {
		t.Fatalf("marks that do not fit are not counted: %q", text)
	}
	parts := []span{{"1 affected", Style{}}, {" · ", Style{}}, {"1 unchecked", Style{}}}
	for n, want := range map[int]string{11: "1 affected…", 14: "1 affected…", 15: "1 affected · 1…", 24: "1 affected · 1 unchecked", 6: "1 aff…"} {
		got := strings.TrimSuffix(cardText([]cardRow{{left: clipSpans(parts, n)}}), "\n")
		if strings.TrimRight(got, " ") != want || width(got) != n {
			t.Errorf("clipped to %d: %q, want %q", n, got, want)
		}
	}
	if got := memoRows("one two three four five six seven", 12, 2); len(got) != 2 || !strings.HasSuffix(got[1].left[1].s, "…") {
		t.Fatal("a long memo is not cut at two lines", cardText(got))
	}
	if got := memoRows("", 20, 2); len(got) != 2 || !strings.Contains(cardText(got), "no notes") {
		t.Fatal("no memo leaves no paper", cardText(got))
	}
	m.Key("s")
	if m.Uniform {
		t.Fatal("s does not bring the wall back")
	}
}

// Enter lays the selected card open over the others, as a wall or a grid:
// everything it says with nothing cut short, the checks its state rests
// on, and the cards left where they were behind it.
func TestServerCardOpensOverTheCards(t *testing.T) {
	for _, uniform := range []bool{false, true} {
		m := previewModel(t)
		m.Colors = colorTrue
		m.Key("3")
		m.Uniform = uniform
		m.Key("end")
		m.draw()
		id := m.Rows()[m.Selected].ID
		top := m.cardsTop
		if top == 0 {
			t.Fatal("the selection did not scroll the cards")
		}
		m.Key("enter")
		out := visible(m)
		for _, want := range []string{"id " + id, "EVIDENCE"} {
			if !strings.Contains(out, want) {
				t.Fatalf("the open card lacks %q (uniform %v):\n%s", want, uniform, out)
			}
		}
		if m.cardsTop != top {
			t.Fatal("opening a card scrolled the cards behind it", top, m.cardsTop)
		}
		if !strings.Contains(out, "SERVERS") || strings.Contains(out, "SERVERS · uniform") != uniform {
			t.Fatal("the panel behind the open card is gone", out)
		}
		// Each check its state rests on leads to it; a click beside the
		// card closes it.
		checks := m.nodeChecks(id)
		if len(checks) == 0 || !strings.Contains(out, checks[0].Name) {
			t.Fatal("the open card does not list its checks", checks)
		}
		click := func(key string) bool {
			for i := len(m.hits) - 1; i >= 0; i-- {
				if h := m.hits[i]; h.key == key {
					m.Key("mouse:0:" + itoa(h.x+1) + ":" + itoa(h.y+1) + ":M")
					return true
				}
			}
			return false
		}
		if !click("key:esc") || m.Detail {
			t.Fatal("a click beside the open card does not close it")
		}
		m.Key("enter")
		m.draw()
		if !click("goto:check:"+checks[0].ID) || m.Tab != tabChecks {
			t.Fatal("a check on the open card does not open it", m.Tab)
		}
	}
}

// An open card taller than the panel scrolls with ↑↓, and says how much
// more there is.
func TestOpenServerCardScrolls(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	m.Width, m.Height = 100, 30
	m.Key("3")
	m.Key("select:node:app")
	m.Key("enter")
	out := visible(m)
	if !strings.Contains(out, "more ") || strings.Contains(out, "EVIDENCE") {
		t.Fatal("a tall open card does not say there is more", out)
	}
	for i := 0; i < 80; i++ {
		m.Key("down")
	}
	out = visible(m)
	if !strings.Contains(out, "EVIDENCE") || strings.Contains(out, "id app") == false {
		t.Fatal("scrolling does not reach the foot of the open card, or loses its band", out)
	}
	m.Key("esc")
	if m.Detail {
		t.Fatal("Esc does not close the open card")
	}
}

// The full card says whether ANSWERED FROM lists day shares or the newest
// reported colos, so neither source claims to be the other.
func TestServerCardAnsweredFromSaysItsSource(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabServers)
	f := m.fleet()
	row := Row{}
	for _, r := range m.Rows() {
		if r.ID == "cf" {
			row = r
		}
	}
	if row.ID == "" {
		t.Fatal("no card for cf")
	}
	card := f.Card("cf")
	if !card.PopsFromRollups {
		t.Fatalf("preview shares are not marked from rollups: %v", card.Pops)
	}
	sections := m.cardSections(f, row, card, cAccent, 100)
	text := ""
	for _, section := range sections {
		text += cardText(section)
	}
	if !strings.Contains(text, "24h shares") || strings.Contains(text, "reported colos") {
		t.Fatalf("rollup shares are labelled %q", text)
	}
	card.Pops, card.PopsFromRollups = []string{"EWR"}, false
	text = ""
	for _, section := range m.cardSections(f, row, card, cAccent, 100) {
		text += cardText(section)
	}
	if !strings.Contains(text, "reported colos") || strings.Contains(text, "24h shares") {
		t.Fatalf("fresh colos are labelled %q", text)
	}
}

// tunnelCardLinks is the "Home Dashboard" box's LINKS section in a preview
// scenario, whose connector check reports on the link to Cloudflare.
func tunnelCardLinks(t *testing.T, scenario string, reading ...string) (*Model, []cardRow) {
	t.Helper()
	m := previewModel(t)
	if scenario != "normal" {
		sim := m.Preview.(*preview.Sim)
		sim.SetScenario(slices.Index(sim.ScenarioIDs(), scenario), m.Now.Add(-30*time.Minute))
		m.Snapshot, _ = sim.Snapshot("", m.Now)
	}
	m.setTab(tabServers)
	f := m.fleet()
	for _, r := range m.Rows() {
		if r.ID != "win" {
			continue
		}
		c := f.Card(r.ID)
		if len(reading) > 0 {
			for i := range c.Links {
				if c.Links[i].ID == "home-cf" {
					c.Links[i].Reading = reading
				}
			}
		}
		for _, section := range m.cardSections(f, r, c, cAccent, 0) {
			if strings.HasPrefix(cardText(section[:1]), "LINKS") {
				return m, section[1:]
			}
		}
	}
	t.Fatal("no LINKS section on the Windows card")
	return nil, nil
}

// padded writes a card row w columns wide: left, then right against the edge.
func padded(left, right string, w int) string {
	return left + strings.Repeat(" ", w-width(left)-width(right)) + right
}

// The row of a link to Cloudflare says the connector's reading beside the
// transport, led by the link's state when it is down; where it does not fit,
// the reading alone, and only then cut short with an ellipsis, the colo list
// before anything it follows.
func TestServerCardLinkRowSaysTheTunnel(t *testing.T) {
	row := func(m *Model, rows []cardRow, lead string, w int) string {
		t.Helper()
		for _, r := range rows {
			if text := rowText(m, r, w); strings.HasPrefix(text, lead) && strings.Contains(text, "Cloudflare") {
				return text
			}
		}
		t.Fatalf("no link row led by %q in\n%s", lead, cardText(rows))
		return ""
	}

	m, rows := tunnelCardLinks(t, "normal")
	for w, want := range map[int]string{
		80: padded("→ Cloudflare", "cloudflared · 4 sessions · hkg,sin", 80),
		47: padded("→ Cloudflare", "cloudflared · 4 sessions · hkg,sin", 47),
		43: padded("→ Cloudflare", "4 sessions · hkg,sin", 43),
		30: "→ Cloudflare 4 sessions · hkg…",
	} {
		if got := row(m, rows, "→", w); got != want {
			t.Errorf("up, %d columns: %q, want %q", w, got, want)
		}
	}

	m, rows = tunnelCardLinks(t, "tunnel-down")
	for w, want := range map[int]string{
		80: padded("✖ → Cloudflare", "cloudflared · no ready session", 80),
		45: padded("✖ → Cloudflare", "cloudflared · no ready session", 45),
		43: padded("✖ → Cloudflare", "no ready session", 43),
		30: "✖ → Cloudflare no ready sessi…",
	} {
		if got := row(m, rows, "✖", w); got != want {
			t.Errorf("down, %d columns: %q, want %q", w, got, want)
		}
	}

	// The API's reading, beside the connector's.
	m, rows = tunnelCardLinks(t, "normal", "4 sessions", "hkg,sin", "API 4 connections healthy")
	for w, want := range map[int]string{
		80: padded("→ Cloudflare", "cloudflared · 4 sessions · hkg,sin · API 4 connections healthy", 80),
		61: padded("→ Cloudflare", "4 sessions · hkg,sin · API 4 connections healthy", 61),
		43: "→ Cloudflare 4 sessions · hkg,sin · API 4…",
	} {
		if got := row(m, rows, "→", w); got != want {
			t.Errorf("API beside the connector, %d columns: %q, want %q", w, got, want)
		}
	}

	// A link no check reported on keeps the plain row: its transport.
	m, rows = tunnelCardLinks(t, "normal")
	plain := ""
	for _, r := range rows {
		if text := rowText(m, r, 60); !strings.Contains(text, "Cloudflare") && strings.HasPrefix(text, "→") {
			plain = text
			break
		}
	}
	if plain == "" || !strings.HasSuffix(plain, "VLESS+Reality · TCP 443") || strings.Contains(plain, "session") {
		t.Fatalf("a link without a reading changed: %q", plain)
	}
}
