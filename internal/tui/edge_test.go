package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// The providers the views know are named in their own spelling by any of
// their names, and any other as written; each of those with a logo has a
// mark and a color too, and its logo is one column, as a terminal counts it.
func TestProviderRegistry(t *testing.T) {
	for in, want := range map[string]string{"cloudflare": "Cloudflare", "Google": "GCP", "oci": "Oracle", "DigitalOcean": "DigitalOcean", "amazon": "AWS", "bandwagon": "bandwagon"} {
		if got := view.ProviderName(in); got != want {
			t.Errorf("%q is named %q, not %q", in, got, want)
		}
	}
	for _, p := range []string{"cloudflare", "vercel", "supabase", "gcp", "azure", "aws", "oracle", "deno", "linode", "digitalocean"} {
		v, ok := view.LookupProvider(p)
		if !ok || v.Glyph == 0 || v.Mark == "" || v.Color == 0 || runeWidth(v.Glyph) != 1 {
			t.Errorf("%s: %+v", p, v)
		}
	}
	if v, ok := view.LookupProvider("netlify"); !ok || v.Glyph != 0 || v.Name != "Netlify" {
		t.Error("a provider without a logo", v)
	}
}

// rowText is what a card row says drawn w columns wide.
func rowText(m *Model, r cardRow, w int) string {
	cv := NewCanvas(w, 1)
	m.drawRow(cv, 0, 0, w, r, cInk)
	var b strings.Builder
	for x := 0; x < w; x++ {
		if c := cv.at(x, 0); !c.cont {
			b.WriteString(c.ch)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// Without a Nerd Font a provider wears nothing, but where an edge in front
// of something has no room for its name, its mark stands in, whole. With
// one, its logo leads a node's name on its card, the maps and the HOST
// column, and an edge's wherever it is named in front of something.
func TestProviderLogosAndMarks(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabServers)
	f := m.fleet()
	row := func(id string) Row {
		i := slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == id })
		if i < 0 {
			t.Fatal("no card for", id)
		}
		return m.Rows()[i]
	}
	via := func(rows []cardRow) cardRow {
		for _, r := range rows {
			if strings.HasPrefix(cardText([]cardRow{{left: r.right}}), "via ") {
				return r
			}
		}
		t.Fatal("no service on the card is reached via an edge", cardText(rows))
		return cardRow{}
	}
	cf, oracle := string(rune(0xe792)), string(rune(0xf0ea1))

	app := m.card(f, row("app"), 40, false)
	blog := via(app)
	if got := rowText(m, blog, 30); !strings.HasSuffix(got, "via CF") || strings.Contains(got, "…") {
		t.Fatalf("narrow, the edge is not its mark: %q", got)
	}
	if got := rowText(m, blog, 80); !strings.HasSuffix(got, "via Cloudflare") {
		t.Fatalf("wide, the edge is not named: %q", got)
	}
	if text := cardText(app); strings.Contains(text, cf) || strings.Contains(text, oracle) {
		t.Fatal("logos without a Nerd Font", text)
	}
	nodes, _, _ := m.graph()
	if i := slices.IndexFunc(nodes, func(n mapNode) bool { return n.ID == "cf" }); nodes[i].Name != "Cloudflare" {
		t.Fatal("a map names a node with a logo without a Nerd Font", nodes[i].Name)
	}

	m.NerdFont = true
	app = m.card(f, row("app"), 40, false)
	if head := cardText(app[:1]); !strings.HasPrefix(head, oracle+" ") {
		t.Fatalf("the card's title does not wear its provider's logo: %q", head)
	}
	blog = via(app)
	if got := rowText(m, blog, 30); !strings.HasSuffix(got, "via "+cf) {
		t.Fatalf("narrow, the edge is not its logo: %q", got)
	}
	if got := rowText(m, blog, 80); !strings.HasSuffix(got, "via "+cf+" Cloudflare") {
		t.Fatalf("wide, the edge's name is not led by its logo: %q", got)
	}
	if text := cardText(m.tile(f, row("vercel"), 46, false)); !strings.Contains(text, "hosted by "+string(rune(0xe8d3))+" Vercel") {
		t.Fatalf("the platform's host is not led by its logo:\n%s", text)
	}
	nodes, _, _ = m.graph()
	if i := slices.IndexFunc(nodes, func(n mapNode) bool { return n.ID == "cf" }); nodes[i].Name != cf+" Cloudflare" {
		t.Fatal("the maps do not lead a node's name with its logo", nodes[i].Name)
	}

	m.setTab(tabServices)
	rows := m.Rows()
	i := slices.IndexFunc(rows, func(r Row) bool { return r.ID == "blog" })
	if got := m.logoRows(rows)[i].Cols[0]; !strings.HasPrefix(got, cf+" Cloudflare ⇢ "+oracle+" ") {
		t.Fatalf("the HOST column does not lead each node with its logo: %q", got)
	}
	if strings.Contains(rows[i].Cols[0], cf) {
		t.Fatal("logos written into the rows themselves", rows[i].Cols[0])
	}
}
