package tui

import (
	"slices"
	"strings"
	"testing"
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
	// An edge reads no resources: the same places, empty.
	cf := m.tile(f, row("cf"), 46, false)
	text = cardText(cf)
	if len(cf) != tileRows || !strings.Contains(text, "not measured") || !strings.Contains(text, "RUNS HERE") || strings.Contains(text, "%") {
		t.Fatalf("the edge's uniform card:\n%s", text)
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
