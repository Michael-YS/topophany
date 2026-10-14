package app

// The read commands print what the TUI shows, so that a script or an agent
// reads what a person reads: status is the Overview, show a tab or the
// evidence behind one entry, history a check's recent samples. They take
// the TUI's connection flags, or under topo preview read its synthetic
// evidence. Text is plain and line-oriented; --json is the same view as
// data.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Exit statuses besides 0, success, and 1, a command that failed.
const (
	ExitAttention   = 2 // topo status: something needs attention
	ExitNoEvidence  = 3 // topo status: nothing current is known
	ExitUnreachable = 4 // no collector answered
)

// ExitError ends topo with Code, reporting Err first when there is one.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "exit status " + strconv.Itoa(e.Code)
}
func (e *ExitError) Unwrap() error { return e.Err }

// reader is where the read commands get evidence.
type reader struct {
	snapshot func(ctx context.Context, observer string) (model.Snapshot, string, error)
	history  func(ctx context.Context, check string, limit int) (client.History, string, error)
}

// unreachable gives a read that no collector answered its exit status.
func unreachable(e error) error {
	if errors.Is(e, client.ErrNoCollector) {
		return &ExitError{ExitUnreachable, e}
	}
	return e
}

func remote(c *client.Client) reader {
	return reader{
		snapshot: func(ctx context.Context, observer string) (model.Snapshot, string, error) {
			s, server, e := c.Snapshot(ctx, observer)
			return s, server, unreachable(e)
		},
		history: func(ctx context.Context, check string, limit int) (client.History, string, error) {
			h, server, e := c.History(ctx, check, limit)
			return h, server, unreachable(e)
		},
	}
}

// previewed is a read under topo preview: the synthetic evidence, the
// moment it is read at, and the defaults its own flags give --observer and
// --json.
type previewed struct {
	config   model.Config
	sim      *preview.Sim
	now      time.Time
	observer string
	json     bool
}

func (p *previewed) reader() reader {
	const server = "synthetic"
	return reader{
		snapshot: func(_ context.Context, observer string) (model.Snapshot, string, error) {
			known := observer == ""
			for _, o := range p.config.Observers {
				known = known || o.ID == observer
			}
			if !known {
				return model.Snapshot{}, "", fmt.Errorf("unknown observer %q", observer)
			}
			s, e := p.sim.Snapshot(observer, p.now)
			if e != nil {
				return s, "", &ExitError{ExitUnreachable, e}
			}
			return s, server, nil
		},
		history: func(_ context.Context, check string, limit int) (client.History, string, error) {
			if _, ok := config.Check(p.config, check); !ok {
				return client.History{}, "", fmt.Errorf("no check %q", check)
			}
			entries, e := p.sim.History(check, limit, p.now)
			if e != nil {
				return client.History{}, "", &ExitError{ExitUnreachable, e}
			}
			// As a collector would serve them: public, with no revision.
			contract := config.PublicContracts(p.config)[check]
			for i := range entries {
				entries[i].Contract, entries[i].Revision = contract, ""
			}
			return client.History{Project: p.config.Project, Revision: config.PublicRevision(p.config), Check: check, Contract: contract, Entries: entries}, server, nil
		},
	}
}

// tabNames are the names show takes, in the TUI's order, then the map.
var tabNames = func() []string {
	out := []string{}
	for _, t := range view.Tabs {
		out = append(out, strings.ToLower(t))
	}
	return append(out, "map")
}()

const mapTab = -1

func tabIndex(name string) (int, bool) {
	for i, t := range tabNames {
		if strings.EqualFold(t, name) {
			if t == "map" {
				return mapTab, true
			}
			return i, true
		}
	}
	return 0, false
}

// parseMixed parses flags that may come before, between or after the
// positional arguments, and returns those. IDs never start with "-".
func parseMixed(f *flag.FlagSet, args []string) ([]string, error) {
	pos := []string{}
	for {
		if e := f.Parse(args); e != nil {
			return nil, e
		}
		if f.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, f.Arg(0))
		args = f.Args()[1:]
	}
}

// read runs status, show or history against the collector the connection
// flags choose or, under topo preview, against its synthetic evidence.
func (a *App) read(ctx context.Context, cmd string, args []string, pv *previewed) error {
	f := a.flags(cmd)
	var connect func() (*client.Client, error)
	observerDefault, jsonDefault := "", false
	if pv == nil {
		connect = a.connection(f)
	} else {
		observerDefault, jsonDefault = pv.observer, pv.json
	}
	observer := new(string)
	if cmd != "history" {
		observer = f.String("observer", observerDefault, "observer view (default all)")
	}
	asJSON := f.Bool("json", jsonDefault, "print the view as JSON")
	limit := new(int)
	if cmd == "history" {
		limit = f.Int("limit", 20, "samples to read, newest first (1..500)")
	}
	pos, e := parseMixed(f, args)
	if e != nil {
		return e
	}
	tab := 0
	switch cmd {
	case "status":
		if len(pos) != 0 {
			return errors.New("usage: topo status [--observer ID] [--json]")
		}
	case "show":
		ok := len(pos) == 1 || len(pos) == 2
		if ok {
			tab, ok = tabIndex(pos[0])
		}
		if !ok || tab == mapTab && len(pos) == 2 {
			return errors.New("usage: topo show TAB [ID] [--observer ID] [--json]; TAB is one of " + strings.Join(tabNames, ", ") + " (the map takes no ID)")
		}
	case "history":
		if len(pos) != 1 || *limit < 1 || *limit > 500 {
			return errors.New("usage: topo history CHECK [--limit 1..500] [--json]")
		}
	}
	var r reader
	if pv == nil {
		c, e := connect()
		if e != nil {
			return e
		}
		defer c.Close()
		r = remote(c)
	} else {
		r = pv.reader()
	}
	now := a.now
	if pv != nil {
		now = func() time.Time { return pv.now }
	}
	if cmd == "history" {
		return a.history(ctx, r, pos[0], *limit, *asJSON, now)
	}
	snap, server, e := r.snapshot(ctx, *observer)
	if e != nil {
		return e
	}
	at := now()
	v := view.View{Snapshot: snap, Now: at, Cached: view.Expired(snap, at)}
	h := header{Project: snap.Project, Instance: snap.Instance, Server: server, Observer: snap.ViewObserver, GeneratedAt: snap.GeneratedAt, Cached: v.Cached}
	if pv != nil {
		h.Scenario, _, _ = strings.Cut(pv.sim.Scenarios()[pv.sim.Scenario()], "\t")
	}
	switch {
	case cmd == "status":
		return a.status(v, h, *asJSON)
	case tab == mapTab:
		return a.showMap(v, h, *asJSON)
	case len(pos) == 2:
		return a.showEntry(v, h, tab, pos[1], *asJSON)
	}
	return a.showTab(v, h, tab, *asJSON)
}

// short is the head of a digest, as revisions and contracts are shown.
func short(digest string) string { return digest[:min(12, len(digest))] }

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// header says where a view comes from, as the TUI's header bar does.
type header struct {
	Project     string    `json:"project"`
	Instance    string    `json:"instance"`
	Server      string    `json:"server"`
	Scenario    string    `json:"scenario,omitempty"` // the synthetic scenario in effect, under topo preview
	Observer    string    `json:"observer"`
	GeneratedAt time.Time `json:"generated_at"`
	Cached      bool      `json:"cached"`
}

func (h header) print(t *text, now time.Time) {
	vantage := h.Observer
	if vantage == "" {
		vantage = "all observers"
	}
	t.kv("Project", h.Project)
	t.kv("Collector", h.Instance+" @ "+h.Server)
	if h.Scenario != "" {
		t.kv("Scenario", h.Scenario)
	}
	t.kv("View", vantage)
	updated := view.Stamp(now, h.GeneratedAt)
	if h.Cached {
		updated += " (cached: nothing current is known)"
	}
	t.kv("Updated", updated)
}

// standing is what topo status concludes, as a word and an exit status:
// clear when the verdict is all clear and nothing needs attention, no
// evidence when nothing current is known, otherwise attention.
func standing(v view.View, attention []view.Row) (string, int) {
	switch v.Verdict().State {
	case "incidents", "down", "degraded":
		return "attention", ExitAttention
	case "clear":
		if len(attention) > 0 {
			return "attention", ExitAttention
		}
		return "clear", 0
	}
	return "no-evidence", ExitNoEvidence
}

// tabBadge is what a tab counts beside its name.
type tabBadge struct {
	Tab   string `json:"tab"`
	Count int    `json:"count"`
	Down  bool   `json:"down"`
}

// statusJSON is the Overview as data. Status says what the exit status
// does; Quiet is what the Overview says when nothing needs attention.
type statusJSON struct {
	header
	Verdict   view.Verdict `json:"verdict"`
	Status    string       `json:"status"`
	Checks    view.Counts  `json:"checks"`
	Incidents int          `json:"incidents"`
	Observers struct {
		Live  int        `json:"live"`
		Total int        `json:"total"`
		Rows  []view.Row `json:"rows"`
	} `json:"observers"`
	Tabs      []tabBadge `json:"tabs"`
	Attention []view.Row `json:"attention"`
	Quiet     *quietJSON `json:"quiet,omitempty"`
	Services  []view.Row `json:"services"`
}

type quietJSON struct {
	State    string `json:"state"`
	Headline string `json:"headline"`
	More     string `json:"more"`
}

func (a *App) status(v view.View, h header, asJSON bool) error {
	attention := v.List(view.Overview, "")
	services := v.List(view.Services, "")
	sort.SliceStable(services, func(i, j int) bool { return view.ServiceRank(services[i]) < view.ServiceRank(services[j]) })
	word, code := standing(v, attention)
	s := statusJSON{header: h, Verdict: v.Verdict(), Status: word, Checks: v.Counts(), Incidents: len(v.Snapshot.Incidents), Attention: attention, Services: services}
	s.Observers.Live, s.Observers.Total = v.Live()
	s.Observers.Rows = v.List(view.Observers, "")
	for i := range view.Tabs {
		n, down := v.Badge(i)
		s.Tabs = append(s.Tabs, tabBadge{tabNames[i], n, down})
	}
	calm, headline, more := v.Calm()
	if len(attention) == 0 {
		s.Quiet = &quietJSON{calm, headline, more}
	}
	done := func(e error) error {
		if e == nil && code != 0 {
			return &ExitError{Code: code}
		}
		return e
	}
	if asJSON {
		return done(printJSON(a.Out, s))
	}
	t := &text{w: a.Out}
	t.line(s.Verdict.Glyph + " " + s.Verdict.Text)
	h.print(t, v.Now)
	t.kv("Checks", fmt.Sprintf("%d up, %d warning, %d down, %d unknown", s.Checks.Up, s.Checks.Warn, s.Checks.Down, s.Checks.Unknown))
	t.kv("Incidents", strconv.Itoa(s.Incidents))
	t.kv("Observers", fmt.Sprintf("%d/%d live", s.Observers.Live, s.Observers.Total))
	look := []string{}
	for i, b := range s.Tabs {
		if b.Count > 0 {
			look = append(look, view.Tabs[i]+" "+strconv.Itoa(b.Count))
		}
	}
	if len(look) == 0 {
		look = []string{"nothing"}
	}
	t.kv("Needs a look", strings.Join(look, ", "))
	t.line()
	if s.Quiet != nil {
		t.line("NEEDS ATTENTION · 0")
		t.line(headline)
		t.line(more)
	} else {
		t.rows("NEEDS ATTENTION", view.Overview, attention)
	}
	t.line()
	t.rows("SERVICES", view.Services, services)
	t.line()
	t.rows("OBSERVERS", view.Observers, s.Observers.Rows)
	return done(t.err)
}

// shown is a row as show prints it: a server's carries its card.
type shown struct {
	view.Row
	Card *view.Card `json:"card,omitempty"`
}

func withCards(v view.View, tab int, rows []view.Row) []shown {
	out := []shown{}
	var f *view.Fleet
	if tab == view.Servers {
		f = v.Fleet()
	}
	for _, r := range rows {
		s := shown{Row: r}
		if f != nil {
			c := f.Card(r.ID)
			s.Card = &c
		}
		out = append(out, s)
	}
	return out
}

type tabJSON struct {
	header
	Tab     string   `json:"tab"`
	Columns []string `json:"columns"`
	Rows    []shown  `json:"rows"`
}

func (a *App) showTab(v view.View, h header, tab int, asJSON bool) error {
	rows := v.List(tab, "")
	if asJSON {
		return printJSON(a.Out, tabJSON{h, tabNames[tab], view.Columns(tab), withCards(v, tab, rows)})
	}
	t := &text{w: a.Out}
	h.print(t, v.Now)
	t.line()
	t.rows(strings.ToUpper(view.Tabs[tab]), tab, rows)
	return t.err
}

// showEntry prints the evidence behind the entries of a tab with an ID,
// given bare or as KIND:ID. IDs are unique only within a kind, so on the
// Overview and Topology tabs a bare ID can name several.
func (a *App) showEntry(v view.View, h header, tab int, id string, asJSON bool) error {
	kind := ""
	if k, rest, ok := strings.Cut(id, ":"); ok {
		kind, id = k, rest
	}
	rows := []view.Row{}
	for _, r := range v.List(tab, "") {
		if r.ID == id && (kind == "" || r.Kind == kind) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return fmt.Errorf("%s has no entry %q; topo show %s lists them", tabNames[tab], secure.Text(id), tabNames[tab])
	}
	entries := withCards(v, tab, rows)
	if asJSON {
		return printJSON(a.Out, tabJSON{h, tabNames[tab], view.Columns(tab), entries})
	}
	t := &text{w: a.Out}
	h.print(t, v.Now)
	for _, e := range entries {
		t.line()
		t.entry(e.Row)
		if e.Card != nil {
			t.card(*e.Card, v.Now)
		}
	}
	return t.err
}

func (a *App) showMap(v view.View, h header, asJSON bool) error {
	nodes, edges := v.Graph()
	type node struct {
		view.MapNode
		Signal string `json:"signal"`
	}
	type edge struct {
		view.MapEdge
		Kind string `json:"kind"`
	}
	ns, es := []node{}, []edge{}
	names := map[string]string{}
	for _, n := range nodes {
		ns = append(ns, node{n, view.NodeSignal(n)})
		names[n.ID] = n.Name
	}
	for _, e := range edges {
		es = append(es, edge{e, view.EdgeKinds[e.Kind]})
	}
	if asJSON {
		return printJSON(a.Out, struct {
			header
			Nodes []node `json:"nodes"`
			Edges []edge `json:"edges"`
		}{h, ns, es})
	}
	t := &text{w: a.Out}
	h.print(t, v.Now)
	t.line()
	t.line(fmt.Sprintf("MAP · %s · %s", view.Plural(len(ns), "node"), view.Plural(len(es), "edge")))
	t.line()
	cells := [][]string{{"SIGNAL", "ID", "NAME", "KIND", "GROUP", "PLACE"}}
	for _, n := range ns {
		place := "no location"
		switch {
		case n.Located && n.Place != "":
			place = n.Place
		case n.Located:
			place = view.LatLon(model.Location{Lat: n.Lat, Lon: n.Lon})
		}
		if n.Inherited {
			place += " (its parent's)"
		}
		cells = append(cells, []string{n.Signal, n.ID, n.Name, view.KindLabel(model.Node{Kind: n.Kind, Provider: n.Provider}), n.Group, place})
	}
	t.table(cells)
	t.line()
	cells = [][]string{{"SIGNAL", "KIND", "FROM", "TO", "ID", "LABEL"}}
	for _, e := range es {
		cells = append(cells, []string{e.Signal, e.Kind, names[e.From], names[e.To], e.ID, e.Label})
	}
	t.table(cells)
	return t.err
}

func (a *App) history(ctx context.Context, r reader, check string, limit int, asJSON bool, now func() time.Time) error {
	h, server, e := r.history(ctx, check, limit)
	if e != nil {
		return e
	}
	if asJSON {
		return printJSON(a.Out, struct {
			client.History
			Server string `json:"server"`
			Check  string `json:"check"`
		}{h, server, check})
	}
	at := now()
	t := &text{w: a.Out}
	t.kv("Project", h.Project)
	t.kv("Collector", server)
	t.kv("Check", check)
	t.kv("Contract", short(h.Contract))
	t.kv("Samples", strconv.Itoa(len(h.Entries))+", newest first")
	t.line()
	cells := [][]string{{"SAMPLED", "AGE", "OUTCOME", "EVIDENCE", "STAGE", "CODE", "ELAPSED", "DETAIL"}}
	for _, o := range h.Entries {
		cells = append(cells, []string{o.SampledAt.Local().Format(view.StampLayout), view.Age(at, o.SampledAt), o.Outcome, o.Evidence, o.Stage, o.Code, strconv.FormatInt(o.DurationMS, 10) + " ms", o.Detail})
	}
	t.table(cells)
	return t.err
}

// scenarios lists the preview's scenarios, the one in effect marked.
func (a *App) scenarios(args []string, list []string, current int, jsonDefault bool) error {
	f := a.flags("scenarios")
	asJSON := f.Bool("json", jsonDefault, "print the scenarios as JSON")
	if e := parsed(f, args); e != nil {
		return e
	}
	type scenario struct {
		Index   int    `json:"index"`
		Name    string `json:"name"`
		Detail  string `json:"detail"`
		Current bool   `json:"current"`
	}
	out := []scenario{}
	for i, s := range list {
		name, detail, _ := strings.Cut(s, "\t")
		out = append(out, scenario{i, name, detail, i == current})
	}
	if *asJSON {
		return printJSON(a.Out, out)
	}
	t := &text{w: a.Out}
	cells := [][]string{{"#", "SCENARIO", "WHAT HAPPENS"}}
	for _, s := range out {
		mark := strconv.Itoa(s.Index)
		if s.Current {
			mark += "*"
		}
		cells = append(cells, []string{mark, s.Name, s.Detail})
	}
	t.table(cells)
	return t.err
}

// text writes plain lines. Every value from a snapshot passes through
// clean, so a hostile name cannot reach the terminal as a control sequence.
type text struct {
	w   io.Writer
	err error
}

func clean(s string) string {
	return secure.Text(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s))
}

func (t *text) line(parts ...string) {
	if t.err != nil {
		return
	}
	_, t.err = io.WriteString(t.w, strings.TrimRight(strings.Join(parts, ""), " ")+"\n")
}

func (t *text) kv(key, value string) { t.line(key, ": ", clean(value)) }

// table aligns cells in columns two spaces apart. A column is as wide as
// its widest cell up to 40; a longer cell runs on rather than being cut,
// and the last column is never padded.
func (t *text) table(cells [][]string) {
	widths := []int{}
	for _, row := range cells {
		for i, c := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], min(40, view.Width(clean(c))))
		}
	}
	for _, row := range cells {
		var b strings.Builder
		for i, c := range row {
			c = clean(c)
			b.WriteString(c)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", max(0, widths[i]-view.Width(c))+2))
			}
		}
		t.line(b.String())
	}
}

// word is a state as the CLI writes it: the TUI's state column, with
// "up" where the TUI leaves a healthy row blank.
func word(r view.Row) string {
	if s := view.State(r); s != "" {
		return s
	}
	return "up"
}

// rows writes a titled table of a tab's rows: the state, the ID, the
// title with its tree guide, and the tab's columns.
func (t *text) rows(title string, tab int, rows []view.Row) {
	t.line(title + " · " + strconv.Itoa(len(rows)))
	if len(rows) == 0 {
		t.line("none")
		return
	}
	cells := [][]string{append([]string{"STATE", "ID"}, view.Columns(tab)...)}
	for _, r := range rows {
		cells = append(cells, append([]string{word(r), r.ID, r.Tree + r.Title}, r.Cols...))
	}
	t.table(cells)
}

// entry writes the evidence behind a row as the TUI's evidence panel has
// it: the title, kind, ID and state, then the detail lines.
func (t *text) entry(r view.Row) {
	t.line(clean(r.Title))
	t.line(clean(r.Kind + " · " + r.ID + " · " + view.Pill(r)))
	t.line()
	blank := true
	for _, l := range r.Details {
		l = strings.TrimRight(clean(l), " ")
		if l == "" {
			if !blank {
				t.line()
			}
			blank = true
			continue
		}
		t.line(l)
		blank = false
	}
}

// card writes what a server's card says beside its evidence.
func (t *text) card(c view.Card, now time.Time) {
	section := func(title string, n int) {
		t.line()
		t.line(title + " · " + strconv.Itoa(n))
	}
	names := func(refs []view.Ref) string {
		out := []string{}
		for _, r := range refs {
			out = append(out, r.Name)
		}
		return strings.Join(out, " ⇢ ")
	}
	t.line()
	t.line("CARD")
	t.kv("Kind", c.Kind)
	t.kv("Group", c.Group)
	t.kv("Where", c.Where)
	if len(c.Services) > 0 {
		section("SERVICES", len(c.Services))
		cells := [][]string{}
		for _, s := range c.Services {
			note, aside := "", s.Runtime
			if s.Affected && s.Health != "down" {
				note = "affected"
			}
			switch {
			case len(s.Via) > 0:
				aside = "via " + names(s.Via)
			case s.Runtime == "" && !s.Monitored:
				aside = "not monitored"
			}
			cells = append(cells, []string{word(view.Row{Health: s.Health, Note: note}), s.ID, s.Name, aside})
		}
		t.table(cells)
	}
	if len(c.Fronts) > 0 {
		section("IN FRONT OF", len(c.Fronts))
		cells := [][]string{}
		for _, ep := range c.Fronts {
			cells = append(cells, []string{word(view.Row{Health: ep.Health}), ep.ID, ep.Address, "→ " + ep.Node.Name})
		}
		t.table(cells)
	}
	if len(c.Pops) > 0 {
		section("ANSWERED FROM", len(c.Pops))
		t.line(clean(strings.Join(c.Pops, " · ")) + " (response headers)")
	}
	if len(c.Addresses) > 0 {
		section("ADDRESSES", len(c.Addresses))
		cells := [][]string{}
		for _, ep := range c.Addresses {
			state := "not measured"
			if ep.Health != "" {
				state = word(view.Row{Health: ep.Health})
			}
			via := ""
			if len(ep.Via) > 0 {
				via = "via " + names(ep.Via)
			}
			cells = append(cells, []string{state, ep.ID, ep.Network, ep.Address, via})
		}
		t.table(cells)
	}
	if len(c.Hosts) > 0 {
		section("HOSTS", len(c.Hosts))
		cells := [][]string{}
		for _, k := range c.Hosts {
			cells = append(cells, []string{word(view.Row{Health: k.Health}), k.ID, k.Name, strings.Join(k.Runs, ", ")})
		}
		t.table(cells)
	}
	if len(c.Observers) > 0 {
		section("OBSERVES FROM HERE", len(c.Observers))
		cells := [][]string{}
		for _, o := range c.Observers {
			cells = append(cells, []string{word(view.Row{Health: o.Health, Note: map[bool]string{true: "outdated"}[o.Outdated]}), o.ID, o.Name, "last seen " + view.Age(now, o.LastSeen)})
		}
		t.table(cells)
	}
	if len(c.Links) > 0 {
		section("LINKS", len(c.Links))
		cells := [][]string{}
		for _, l := range c.Links {
			arrow := "→"
			if l.Inbound {
				arrow = "←"
			}
			cells = append(cells, []string{arrow, l.ID, l.Other.Name, l.Transport})
		}
		t.table(cells)
	}
	if c.Memo != "" {
		t.line()
		t.line("MEMO")
		for _, l := range strings.Split(strings.ReplaceAll(c.Memo, "\r\n", "\n"), "\n") {
			t.line(clean(l))
		}
	}
}
