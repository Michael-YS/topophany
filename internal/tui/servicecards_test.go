package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// s lays the services out as cards under their groups' headings, keeping
// the one selected, and s again brings the list back; the cards are all
// one size and never fold away.
func TestServiceCardsMode(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	m.Height = 60
	m.setTab(tabServices)
	if out := visible(m); !strings.Contains(out, "Cards") || strings.Contains(out, "SERVICES · cards") {
		t.Fatal("the list does not offer the cards", out)
	}
	m.Key("select:service:blog")
	m.Key("s")
	if !m.ServiceCards || m.Uniform {
		t.Fatal("s does not lay the services out as cards")
	}
	if r, ok := m.selectedRow(); !ok || r.ID != "blog" {
		t.Fatal("the cards lost the selection", r)
	}
	rows := m.Rows()
	if slices.ContainsFunc(rows, func(r Row) bool { return r.Kind == "group" }) || len(rows) != len(m.Snapshot.Topology.Services) {
		t.Fatal("the cards fold, or list groups as cards", len(rows))
	}
	out := visible(m)
	for _, want := range []string{"SERVICES · cards", "groups", "List", "SEEN FROM", "DEPENDS ON"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the cards lack %q:\n%s", want, out)
		}
	}
	heights := cardHeights(m.draw())
	if len(heights) < 3 || slices.Min(heights) != tileRows+2 || slices.Max(heights) != tileRows+2 {
		t.Fatal("service cards differ in height", heights)
	}
	// The selection's heading shows with it.
	f := m.fleet()
	c, _ := f.ServiceCard("blog")
	if !strings.Contains(out, c.Application.Name) {
		t.Fatalf("the selection's group heading is not over it:\n%s", out)
	}
	// Collapsed groups do not hide cards.
	m.GroupsCollapsed = true
	if len(m.Rows()) != len(rows) {
		t.Fatal("collapsed groups fold the cards")
	}
	m.GroupsCollapsed = false
	m.Key("select:service:postgres")
	m.Key("s")
	if m.ServiceCards {
		t.Fatal("s does not bring the list back")
	}
	if r, ok := m.selectedRow(); !ok || r.ID != "postgres" {
		t.Fatal("the list lost the selection", r)
	}
	m.setTab(tabServers)
	m.Key("s")
	if !m.Uniform || m.ServiceCards {
		t.Fatal("s on Servers changes the services")
	}
}

// A card says the same things in the same places: its name and where it
// runs, who sees it from where, its tallies, the address it is known by
// and its memo; one no check measures says so rather than charting nothing.
func TestServiceCardRows(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabServices)
	m.ServiceCards = true
	f := m.fleet()
	row := func(id string) Row {
		i := slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == id })
		if i < 0 {
			t.Fatal("no card for", id)
		}
		return m.Rows()[i]
	}
	blog := m.serviceTile(f, row("blog"), 46, false)
	if len(blog) != tileRows {
		t.Fatal("a service card has", len(blog), "rows")
	}
	text := cardText(blog)
	for _, want := range []string{"SEEN FROM 3", "p50", "24h", " ms", "CHECKS", "ENDPOINTS", "DEPENDS ON", "all passing", "public", "www.example.invalid", "⇠", "+1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("blog's card lacks %q:\n%s", want, text)
		}
	}
	for _, r := range blog[3:7] {
		if r.left == nil {
			continue
		}
		if w := spansWidth(r.left); w > 46 {
			t.Fatalf("a seen-from row is %d wide, over 46: %q", w, cardText([]cardRow{r}))
		}
	}
	backup := m.serviceTile(f, row("backup-job"), 46, false)
	text = cardText(backup)
	if len(backup) != tileRows || !strings.Contains(text, "not monitored") || !strings.Contains(text, "unmeasured") || !strings.Contains(text, "no endpoint declared") || strings.Contains(text, "SEEN FROM") {
		t.Fatalf("an unmonitored service's card:\n%s", text)
	}
	// A narrow card cuts the address to its host before the colos give way.
	narrow := cardText([]cardRow{m.serviceKnownAt(f, mustCard(t, m, "blog"), 30)})
	if strings.Contains(narrow, "https://") || !strings.Contains(narrow, "www.example.invalid") {
		t.Fatalf("a narrow card's address: %q", narrow)
	}
}

func mustCard(t *testing.T, m *Model, id string) view.ServiceCard {
	t.Helper()
	c, ok := m.fleet().ServiceCard(id)
	if !ok {
		t.Fatal("no card for", id)
	}
	return c
}

// Enter lays the selected card open over the others: who sees it, laid
// out check by check, how it is reached, where the edge answered from,
// where it runs and what it depends on; each leads where it says.
func TestServiceCardOpens(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	m.Height = 60
	m.setTab(tabServices)
	m.Key("s")
	m.Key("select:service:blog")
	m.draw()
	top := m.cardsTop
	m.Key("enter")
	if !m.Detail {
		t.Fatal("enter does not open the card")
	}
	out := visible(m)
	for _, want := range []string{"id blog", "SEEN FROM", "a slot per", "p95", "REACHED AT", "ANSWERED FROM", "24h shares", "RUNS ON", "DEPENDS ON", "SERVICES · cards"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the open card lacks %q:\n%s", want, out)
		}
	}
	if m.cardsTop != top {
		t.Fatal("opening a card scrolled the cards behind it", top, m.cardsTop)
	}
	click := func(prefix string) bool {
		for i := len(m.hits) - 1; i >= 0; i-- {
			if h := m.hits[i]; strings.HasPrefix(h.key, prefix) {
				m.Key("mouse:0:" + itoa(h.x+1) + ":" + itoa(h.y+1) + ":M")
				return true
			}
		}
		return false
	}
	if !click("select:service:postgres") {
		t.Fatal("what it depends on does not lead to it")
	}
	if r, _ := m.selectedRow(); r.ID != "postgres" || m.Tab != tabServices {
		t.Fatal("a dependency's row selects", r.ID)
	}
	m.Key("select:service:blog")
	m.Detail = true
	m.draw()
	if !click("goto:server:app") || m.Tab != tabServers {
		t.Fatal("where it runs does not lead to its server", m.Tab)
	}
	if r, _ := m.selectedRow(); r.ID != "app" {
		t.Fatal("its server is not selected", r.ID)
	}
	m.setTab(tabServices)
	m.Key("select:service:blog")
	m.Detail = true
	m.draw()
	if !click("goto:check:") || m.Tab != tabChecks {
		t.Fatal("a check on the open card does not open it", m.Tab)
	}
}

// While the cards show, the checks of the services on them read their
// history, so their sparks start full rather than a sample at a time, and
// each snapshot after adds its newest; the list reads none.
func TestServiceCardsReadTheirChecksHistory(t *testing.T) {
	m := previewModel(t)
	sim := m.Preview.(*preview.Sim)
	read := func() []string {
		ids := []string{}
		for id := m.wantTrend(); id != ""; id = m.wantTrend() {
			entries, e := sim.History(id, view.StripSamples, m.Now)
			m.KeepTrend(id, entries, e)
			ids = append(ids, id)
		}
		return ids
	}
	read()
	m.setTab(tabServices)
	if ids := read(); len(ids) != 0 {
		t.Fatal("the list reads its services' history", ids)
	}
	m.Key("s")
	ids := read()
	blog := mustCard(t, m, "blog")
	for _, s := range blog.SeenFrom {
		for _, x := range s.Checks {
			if !slices.Contains(ids, x.ID) {
				t.Fatal("the cards do not read the history of", x.ID, ids)
			}
		}
	}
	x := blog.SeenFrom[0].Checks[0]
	if spark := cardText([]cardRow{{left: m.spark(x, 24, cInk)}}); !strings.ContainsAny(spark, string(sparks[1:])) {
		t.Fatalf("a check with history draws no bars: %q", spark)
	}
	before := len(m.trendSamples(x.ID))
	m.Now = m.Now.Add(time.Minute)
	snap, e := sim.Snapshot("", m.Now)
	if e != nil {
		t.Fatal(e)
	}
	m.Snapshot = snap
	m.absorb()
	if after := len(m.trendSamples(x.ID)); after <= before && before < trendKeep {
		t.Fatal("a new snapshot's sample does not join the spark", before, after)
	}
}

// Scoped to one observer, a card of a service only others measure says it
// is not seen from here, not that nothing monitors it.
func TestScopedServiceCardSaysNotSeenFromHere(t *testing.T) {
	m := previewModel(t)
	sim := m.Preview.(*preview.Sim)
	snap, err := sim.Snapshot("obs-mac", m.Now)
	if err != nil {
		t.Fatal(err)
	}
	m.Snapshot, m.Observer = snap, "obs-mac"
	m.setTab(tabServices)
	m.ServiceCards = true
	f := m.fleet()
	c, _ := f.ServiceCard("vless-transit")
	i := slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == "vless-transit" })
	text := cardText(m.serviceTile(f, m.Rows()[i], 46, false))
	if !strings.Contains(text, "not seen from here") || !strings.Contains(text, "none here") || strings.Contains(text, "not monitored") || !c.Elsewhere {
		t.Fatalf("a scoped card:\n%s", text)
	}
}
