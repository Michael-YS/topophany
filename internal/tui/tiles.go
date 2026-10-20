package tui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The uniform grid (s): every card the same size, saying the same things
// in the same places, so the cards read across as well as down. Under the
// header, a panel each for CPU, memory and disk; then, in the same three
// columns, how many services run there, how many addresses reach it and
// how many checks measure it, a glyph for each in its state; then the
// address it is known by and the first lines of its memo. What a server
// does not have stays in its place, empty, and Enter lays the card open
// for everything else.

// tileRows is how many rows a uniform card holds inside its frame.
const tileRows = 15

// tile builds the rows of a server's uniform card for content width w.
func (m *Model) tile(f fleet, r Row, w int, selected bool) []cardRow {
	c := f.Card(r.ID)
	rows, _ := m.cardHead(f, r, c, selected)
	rows = append(rows, cardRow{})
	rows = append(rows, m.tilePanel(f, r, c, w)...)
	rows = append(rows, cardRow{})
	rows = append(rows, m.tallies(f, r, c, w)...)
	rows = append(rows, cardRow{}, m.knownAt(c))
	return append(rows, memoRows(c.Memo, w, 2)...)
}

// tilePanel is the four rows under a card's header: a host's resources as
// read. An edge or a platform has no machine of yours to read, so in their
// place it says what it stands in front of and where it answered from; a
// host nothing reads says why where the charts would be.
func (m *Model) tilePanel(f fleet, r Row, c view.Card, w int) []cardRow {
	if rd := reading(c.Resources); rd != nil {
		return m.resourcePanel(*rd, w)
	}
	n := f.Nodes[r.ID]
	switch {
	case hosted(n.Kind):
		return m.edgePanel(n, c)
	case len(c.Observers) == 0:
		return quietPanel("CPU, memory and disk unmeasured", quiet("no worker runs here"))
	}
	return quietPanel("CPU, memory and disk unmeasured", quiet("its worker runs no host check"))
}

// edgePanel lists the addresses an edge answers for, each with the server
// behind it, and where it answered from; with none in front of anything,
// who hosts it.
func (m *Model) edgePanel(n model.Node, c view.Card) []cardRow {
	pops := strings.Join(c.Pops, " · ")
	if len(c.Regions) > 0 {
		if pops != "" {
			pops += " · "
		}
		pops += "edge region " + strings.Join(c.Regions, " · ")
	}
	if len(c.Fronts) == 0 {
		host := []span{quiet("a managed " + n.Kind)}
		if p := view.ProviderName(n.Provider); p != "" {
			host = append(append([]span{quiet("hosted by ")}, m.logo(n.Provider)...), quiet(p))
		}
		if pops == "" {
			return quietPanel("no machine of yours to measure", host...)
		}
		return quietPanel("answered from "+pops, host...)
	}
	rows := []cardRow{{left: []span{{"IN FRONT OF", fg(cMuted)}}, right: []span{{itoa(len(c.Fronts)), fg(cText)}}}}
	room := 3
	if pops != "" {
		room = 2
	}
	for i, ep := range c.Fronts {
		if i == room-1 && len(c.Fronts) > room {
			rows = append(rows, cardRow{left: []span{{"+" + itoa(len(c.Fronts)-i) + " more", fg(cFaint)}}})
			break
		}
		g, gc := endpointGlyph(ep.Health)
		rows = append(rows, cardRow{left: []span{{string(g) + " ", fg(gc)}, {hostOf(ep.Address), fg(cText)}}, right: []span{{"→ " + ep.Node.Name, fg(cMuted)}}, hit: "select:endpoint:" + ep.ID})
	}
	if pops != "" {
		hue := Mix(providerColor(n.Provider, cMuted), cText, .25)
		rows = append(rows, cardRow{left: []span{{"answered from ", fg(cFaint)}, {pops, fg(hue)}}})
	}
	for len(rows) < 4 {
		rows = append(rows, cardRow{})
	}
	return rows
}

// quietPanel stands in for a panel with nothing to show, saying what is
// missing and why.
func quietPanel(why string, what ...span) []cardRow {
	return []cardRow{
		{},
		{left: append([]span{{"◌ ", fg(cFaint)}}, what...)},
		{left: []span{{"  " + why, fg(cFaint).italic()}}},
		{},
	}
}

// quiet is what a quiet panel says is missing.
func quiet(s string) span { return span{s, fg(cMuted).italic()} }

// reading is the resources a uniform card shows of those read on a host:
// the first current, else the first; nil with none.
func reading(rs []view.CardResource) *view.CardResource {
	if i := slices.IndexFunc(rs, func(r view.CardResource) bool { return r.Facts != nil }); i >= 0 {
		return &rs[i]
	}
	if len(rs) > 0 {
		return &rs[0]
	}
	return nil
}

// mark is one thing a tally counts, as a glyph in its state's color.
type mark struct {
	glyph rune
	color Color
}

// tally is a panel under the resources: its label and count, a mark for
// each thing counted, and what of them needs a look in words.
type tally struct {
	label string
	marks []mark
	say   []span
	hit   string
}

// tallies are three panels in the columns of the resources above: what
// runs on a server, how it is reached (for an edge, what it answers for)
// and the checks its state rests on.
func (m *Model) tallies(f fleet, r Row, c view.Card, w int) []cardRow {
	return tallyRows([3]tally{m.serviceTally(f, r, c), addressTally(c), m.checkTally(r)}, w)
}

// tallyRows lays three tallies out in the columns of a card's panel above,
// w wide.
func tallyRows(ts [3]tally, w int) []cardRow {
	const gap = 2
	pw := (w - 2*gap) / 3
	extra := w - 2*gap - 3*pw
	rows := make([]cardRow, 3)
	for i, t := range ts {
		n := pw
		if i == 2 {
			n += extra
		}
		if i > 0 {
			for k := range rows {
				rows[k].left = append(rows[k].left, span{"  ", Style{}})
			}
		}
		count, cst := itoa(len(t.marks)), fg(cText)
		if len(t.marks) == 0 {
			cst = fg(cFaint)
		}
		rows[0].left = append(rows[0].left, span{fit(t.label, n-width(count)), fg(cMuted)}, span{count, cst})
		rows[1].left = append(rows[1].left, markSpans(t.marks, n)...)
		rows[2].left = append(rows[2].left, clipSpans(t.say, n)...)
	}
	return rows
}

// clipSpans fits spans into n columns: whole while they fit, then the first
// that does not cut short with "…", or left out where it would keep a count
// without its word, and no separator left hanging before the "…"; padded
// out to n.
func clipSpans(spans []span, n int) []span {
	out := []span{}
	used := 0
	sep := func(s span) bool { return strings.Trim(s.s, " ·") == "" }
	for _, s := range spans {
		if w := width(s.s); used+w <= n {
			out = append(out, s)
			used += w
			continue
		}
		cut := strings.TrimRight(fit(s.s, n-used-1), " ·")
		if len(out) > 0 && !strings.ContainsFunc(cut, unicode.IsLetter) {
			// A count without its word, as "1" of "1 unknown", says
			// nothing: the whole parts before it end the line.
			cut = ""
		}
		s.s = cut
		if s.s == "" {
			for len(out) > 0 && sep(out[len(out)-1]) {
				used -= width(out[len(out)-1].s)
				out = out[:len(out)-1]
			}
		}
		s.s += "…"
		out = append(out, s)
		used += width(s.s)
		break
	}
	if used < n {
		out = append(out, span{fit("", n-used), Style{}})
	}
	return out
}

// markSpans lays marks out n columns wide: spaced while they fit, then
// packed, then as many as fit before a count of the rest.
func markSpans(marks []mark, n int) []span {
	out := []span{}
	used := 0
	put := func(s string, st Style) {
		out = append(out, span{s, st})
		used += width(s)
	}
	step := 2
	if 2*len(marks)-1 > n {
		step = 1
	}
	shown := len(marks)
	if len(marks) > n {
		shown = max(0, n-1-len(itoa(len(marks))))
	}
	for i, mk := range marks[:shown] {
		if i > 0 && step == 2 {
			put(" ", Style{})
		}
		put(string(mk.glyph), fg(mk.color))
	}
	if shown < len(marks) {
		put("+"+itoa(len(marks)-shown), fg(cFaint))
	}
	if len(marks) == 0 {
		put("·", fg(cLine))
	}
	if used < n {
		put(fit("", n-used), Style{})
	}
	return out
}

// counted says what of a tally needs a look: each state that has any, in
// the order given, as "1 down · 2 unknown"; else what all of them are.
func counted(parts []part, all string, allColor Color) []span {
	out := []span{}
	for _, p := range parts {
		if p.n == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, span{" · ", fg(cFaint)})
		}
		out = append(out, span{itoa(p.n) + " " + p.word, fg(p.c)})
	}
	if len(out) == 0 {
		return []span{{all, fg(allColor)}}
	}
	return out
}

// part is how many of a tally are in a state, and its word and color.
type part struct {
	n    int
	word string
	c    Color
}

// serviceTally counts what runs on a server, trouble first as its card
// lists it.
func (m *Model) serviceTally(f fleet, r Row, c view.Card) tally {
	t := tally{label: "SERVICES"}
	if hosted(f.Nodes[r.ID].Kind) {
		t.label = "RUNS HERE"
	}
	down, warn, unknown, unwatched := 0, 0, 0, 0
	for _, v := range c.Services {
		mk := mark{healthGlyph(v.Health), healthColor(v.Health)}
		switch {
		case v.Health == "down":
			down++
		case v.Affected || v.Health == "degraded":
			mk = mark{'▲', cWarn}
			warn++
		case !v.Monitored:
			mk.color = cFaint
			unwatched++
		case v.Health != "up":
			unknown++
		}
		t.marks = append(t.marks, mk)
	}
	t.say = counted([]part{{down, "down", cDown}, {warn, "affected", cWarn}, {unknown, "unknown", cMuted}, {unwatched, "unchecked", cFaint}}, "all up", Mix(cUp, cMuted, .4))
	if len(c.Services) == 0 {
		t.say = []span{{"none declared", fg(cFaint)}}
	}
	return t
}

// addressTally counts how a server is reached, by health, and names the
// networks; an edge's, the addresses it answers for.
func addressTally(c view.Card) tally {
	t := tally{label: "ADDRESSES"}
	eps := c.Addresses
	if len(eps) == 0 && len(c.Fronts) > 0 {
		t.label, eps = "FRONTS", c.Fronts
	}
	nets := []string{}
	for _, ep := range eps {
		g, gc := endpointGlyph(ep.Health)
		t.marks = append(t.marks, mark{g, gc})
		if !slices.Contains(nets, ep.Network) && ep.Network != "" {
			nets = append(nets, ep.Network)
		}
	}
	for i, n := range nets {
		if i > 0 {
			t.say = append(t.say, span{" ", Style{}})
		}
		t.say = append(t.say, span{n, fg(Mix(netColor(n), cInk, .2))})
	}
	if len(eps) == 0 {
		t.say = []span{{"none declared", fg(cFaint)}}
	}
	return t
}

// checkTally counts the checks a server's state rests on, by health.
func (m *Model) checkTally(r Row) tally {
	t := tally{label: "CHECKS"}
	down, warn, unknown := 0, 0, 0
	for _, x := range m.nodeChecks(r.ID) {
		t.marks = append(t.marks, mark{healthGlyph(x.Health), healthColor(x.Health)})
		switch x.Health {
		case "up":
		case "down":
			down++
		case "degraded":
			warn++
		default:
			unknown++
		}
	}
	// Trouble first, as everywhere else.
	slices.SortStableFunc(t.marks, func(a, b mark) int { return markRank(a) - markRank(b) })
	t.say = counted([]part{{down, "failing", cDown}, {warn, "degraded", cWarn}, {unknown, "unknown", cMuted}}, "all passing", Mix(cUp, cMuted, .4))
	if len(t.marks) == 0 {
		t.say = []span{{"unmeasured", fg(cFaint)}}
	}
	return t
}

func markRank(mk mark) int {
	switch mk.glyph {
	case '✖':
		return 0
	case '▲':
		return 1
	case '○':
		return 2
	}
	return 3
}

// knownAt is the address a server is known by, on one line: its first
// public one, else its first; for an edge, the first it answers for; and
// how many more there are.
func (m *Model) knownAt(c view.Card) cardRow {
	eps, front := c.Addresses, false
	if len(eps) == 0 && len(c.Fronts) > 0 {
		eps, front = c.Fronts, true
	}
	if len(eps) == 0 {
		return cardRow{left: []span{{"no address declared", fg(cFaint).italic()}}}
	}
	i := max(0, slices.IndexFunc(eps, func(ep view.CardEndpoint) bool { return ep.Network == "public" }))
	ep := eps[i]
	g, gc := endpointGlyph(ep.Health)
	right := []span{{string(g), fg(gc)}}
	if len(eps) > 1 {
		right = append(right, span{" +" + itoa(len(eps)-1), fg(cFaint)})
	}
	left := []span{{fit(ep.Network, 9), fg(netColor(ep.Network))}, {ep.Address, fg(cText)}}
	if front {
		left = []span{{"→ ", fg(cAccent)}, {hostOf(ep.Address), fg(cText)}, {"  " + ep.Node.Name, fg(cMuted)}}
	}
	return cardRow{left: left, right: right, hit: "select:endpoint:" + ep.ID}
}
