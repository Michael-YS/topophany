// Package tui is a terminal UI. In viewer mode it is remote-only, with no probe
// or persistence responsibilities; losing this client does not stop monitoring.
// In editor mode (topo edit) it edits a local reviewed topology file and never
// talks to a collector.
package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var tabs = []string{"Overview", "Topology", "Servers", "Services", "Paths", "Checks", "Incidents", "Observers", "About"}

const (
	tabOverview = iota
	tabTopology
	tabServers
	tabServices
	tabPaths
	tabChecks
	tabIncidents
	tabObservers
	tabAbout
)

// Row is one selectable entry of a list. Cols are aligned secondary columns
// (headed by tabColumns); Note overrides the state column, e.g. "affected";
// Tree is the guide drawn before the title in the Topology hierarchy.
type Row struct {
	Kind, ID, Health, Title, Meta string
	Cols                          []string
	Note, Tree                    string
	Details                       []string
}
type Model struct {
	Snapshot              model.Snapshot
	Server                string
	Error                 string
	Loading               string // a request in flight while nothing current is shown
	Observer              string
	Tab, Selected, Offset int
	Width, Height         int
	Search                string
	Searching             bool
	Detail                bool
	NoColor               bool
	Colors                int // colorTrue or color256
	Now                   time.Time
	MapMode               string // "", "virtual", "geo" or "network"; "" picks virtual when locations exist
	Zoom                  int
	Edit                  *Editor // non-nil only for topo edit
	Preview               Preview // non-nil only for topo preview
	Debug                 bool    // performance/geometry overlay (D)
	Transparent           bool    // keep the terminal background (TOPO_BACKGROUND=terminal)
	Hyperlinks            bool    // write links as OSC 8 hyperlinks
	perf                  perf
	modal                 *modal
	notice                string
	noticeErr             bool
	noticeAt              time.Time
	aboutAt               time.Time
	aboutSpin             float64 // extra globe rotation from the mouse wheel
	vcache                vCache  // the virtual map's wires, kept while its layout holds
	hits                  []hit
	drawingModal          bool
}
type hit struct {
	x, y, w int
	key     string
	modal   bool
}

func (m *Model) hit(x, y, w int, key string) {
	m.hits = append(m.hits, hit{x, y, w, key, m.drawingModal})
}
func (m *Model) st(s Style) Style { return s }

func (m *Model) Cached() bool {
	if m.Edit != nil {
		return false
	}
	return m.Error != "" || m.Snapshot.GeneratedAt.IsZero() || m.Now.Sub(m.Snapshot.GeneratedAt) > 15*time.Second
}
func (m *Model) say(msg string, isErr bool) {
	m.notice, m.noticeErr, m.noticeAt = secure.Text(msg), isErr, m.Now
}

// Rows lists the current tab, filtered by the search and marked when the
// view is cached or a local draft.
func (m *Model) Rows() []Row {
	rows := m.tabRows(m.Tab)
	filtered := rows[:0]
	search := strings.ToLower(m.Search)
	for _, r := range rows {
		if search == "" || strings.Contains(strings.ToLower(r.ID+" "+r.Title+" "+r.Meta+" "+strings.Join(r.Cols, " ")), search) {
			if m.Cached() {
				r.Health = "unknown"
				r.Note = "cached"
				r.Meta = "CACHED · " + r.Meta
				r.Details = append([]string{"CACHED VIEW — current status not established.", ""}, r.Details...)
			}
			if m.Edit != nil {
				r.Details = append([]string{"LOCAL DRAFT — no live evidence in the editor.", ""}, r.Details...)
			}
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// needsAttention is true for rows a person should look at: confirmed or
// partial failures, entities on a failing path, and silent observers.
func needsAttention(r Row) bool {
	switch {
	case r.Kind == "incident", r.Health == "down", r.Health == "degraded", r.Note == "affected":
		return true
	case r.Kind == "observer":
		return r.Health != "up"
	}
	return false
}

func attentionRank(r Row) int {
	switch {
	case r.Kind == "incident":
		return 0
	case r.Health == "down":
		return 1
	case r.Health == "degraded":
		return 2
	case r.Kind == "observer":
		return 3
	}
	return 4
}

// incidentTitle names an incident by who saw it and what failed, e.g.
// "长沙 Windows → 主链 DMIT → AAITR 家宽" for scope cs-win/path/w-dmit-aaitr.
func (m *Model) incidentTitle(i model.Incident) string {
	t := m.Snapshot.Topology
	name := func(id string, list func(string) string) string {
		if n := list(id); n != "" {
			return n
		}
		return id
	}
	observer := func(id string) string {
		for _, o := range t.Observers {
			if o.ID == id {
				return o.Name
			}
		}
		return ""
	}
	path := func(id string) string {
		for _, p := range t.Paths {
			if p.ID == id {
				return p.Name
			}
		}
		return ""
	}
	node := func(id string) string {
		for _, n := range t.Nodes {
			if n.ID == id {
				return n.Name
			}
		}
		return ""
	}
	check := func(id string) string {
		for _, c := range t.Checks {
			if c.ID == id {
				return c.Name
			}
		}
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(i.Scope, "/policy"), "/")
	what := ""
	switch {
	case len(parts) >= 3 && parts[1] == "path":
		what = name(parts[2], path)
	case len(parts) >= 3 && parts[1] == "asset":
		what = name(parts[2], node)
	case len(parts) >= 2:
		what = name(parts[1], check)
	default:
		return i.Scope
	}
	title := name(parts[0], observer) + " → " + what
	if strings.HasSuffix(i.Scope, "/policy") {
		title += " · policy"
	}
	return title
}

// tabRows builds the unfiltered rows of a tab.
func (m *Model) tabRows(tab int) []Row {
	s := m.Snapshot
	rows := []Row{}
	nodeNames := map[string]string{}
	for _, n := range s.Topology.Nodes {
		nodeNames[n.ID] = n.Name
		if n.Name == "" {
			nodeNames[n.ID] = n.ID
		}
	}
	checkDefs := map[string]model.Check{}
	for _, c := range s.Topology.Checks {
		checkDefs[c.ID] = c
	}
	observerNames := map[string]string{}
	for _, o := range s.Topology.Observers {
		observerNames[o.ID] = o.Name
		if o.Name == "" {
			observerNames[o.ID] = o.ID
		}
	}
	entity := func(kind string, e model.EntityState, title, meta string, details []string) Row {
		r := Row{Kind: kind, ID: e.ID, Health: e.Health, Title: title, Meta: meta, Details: append([]string{"ID: " + e.ID, "State: " + e.Health, "Evidence: " + e.Evidence, "Affected dependency: " + fmt.Sprint(e.Affected), "", e.Reason, "Checks: " + strings.Join(e.Checks, ", ")}, details...)}
		if e.Affected && e.Health != "down" {
			r.Note = "affected"
		}
		return r
	}
	index := func(list []model.EntityState) map[string]model.EntityState {
		out := make(map[string]model.EntityState, len(list))
		for _, e := range list {
			out[e.ID] = e
		}
		return out
	}
	nodeStates, linkStates := index(s.Nodes), index(s.Links)
	find := func(list map[string]model.EntityState, id string) model.EntityState {
		if e, ok := list[id]; ok {
			return e
		}
		return model.EntityState{ID: id, Health: "unknown", Evidence: "unknown", Reason: "no state in this view"}
	}
	// node is a node's row with the evidence for it: what and where it is,
	// its notes, how it is reached and what it answers for.
	node := func(n model.Node) Row {
		place := "Location: not declared"
		switch l := n.Location; {
		case l != nil:
			place = fmt.Sprintf("Location: %s (%.2f, %.2f) — declared, not inferred from IP", l.Label, l.Lat, l.Lon)
		case n.Kind == "edge":
			place = "Location: anycast edge — answers from many places"
		}
		details := []string{"", "Node identity: " + n.ID, "Group: " + n.Group, "Kind: " + n.Kind, "Provider: " + providerName(n.Provider), "Parent: " + n.Parent, place, "", n.Notes, "", "Access endpoints:"}
		fronts := []string{}
		for _, ep := range s.Topology.Endpoints {
			if ep.Node == n.ID {
				details = append(details, ep.ID+" ["+ep.Network+"] "+ep.Address+via(ep))
			}
			if contains(ep.Via, n.ID) {
				fronts = append(fronts, ep.ID+" → "+nodeNames[ep.Node])
			}
		}
		if len(fronts) > 0 {
			details = append(details, "", "Answers first for:")
			details = append(details, fronts...)
		}
		return entity("node", find(nodeStates, n.ID), nodeNames[n.ID], n.Group, details)
	}
	incident := func(i model.Incident) Row {
		return Row{Kind: "incident", ID: i.ID, Health: "down", Title: m.incidentTitle(i), Meta: i.Summary, Cols: []string{age(m.Now, i.Since), plural(len(i.Checks), "check")}, Details: incidentDetails(i)}
	}
	switch tab {
	case tabOverview:
		// Everything that needs a look, worst first: incidents, failures,
		// partial failures, silent observers, then whatever sits on a
		// failing path. Nothing here means nothing needs attention.
		for _, i := range s.Incidents {
			r := incident(i)
			r.Cols = []string{"incident"}
			rows = append(rows, r)
		}
		kinds := map[int]string{tabServices: "service", tabPaths: "path", tabTopology: "node", tabObservers: "observer"}
		for _, t := range []int{tabServices, tabPaths, tabTopology, tabObservers} {
			if t == tabObservers && m.Edit != nil {
				continue
			}
			for _, r := range m.tabRows(t) {
				if r.Kind == kinds[t] && needsAttention(r) {
					r.Tree = ""
					r.Cols = []string{r.Kind}
					rows = append(rows, r)
				}
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return attentionRank(rows[i]) < attentionRank(rows[j]) })
	case tabTopology:
		checkHealth := map[string][]string{}
		for _, cs := range s.Checks {
			if d, ok := checkDefs[cs.ID]; ok && d.Endpoint != "" {
				checkHealth[d.Endpoint] = append(checkHealth[d.Endpoint], cs.Health)
			}
		}
		known := map[string]bool{}
		children := map[string][]model.Node{}
		for _, n := range s.Topology.Nodes {
			known[n.ID] = true
		}
		roots := []model.Node{}
		for _, n := range s.Topology.Nodes {
			if n.Parent != "" && known[n.Parent] && n.Parent != n.ID {
				children[n.Parent] = append(children[n.Parent], n)
			} else {
				roots = append(roots, n)
			}
		}
		seen := map[string]bool{}
		// walk lists a node, then its endpoints, then the nodes it hosts.
		var walk func(n model.Node, guide, branch string)
		walk = func(n model.Node, guide, branch string) {
			if seen[n.ID] {
				return
			}
			seen[n.ID] = true
			eps := []model.Endpoint{}
			for _, ep := range s.Topology.Endpoints {
				if ep.Node == n.ID {
					eps = append(eps, ep)
				}
			}
			r := node(n)
			r.Tree = guide + branch
			r.Cols = []string{n.Group, kindLabel(n)}
			rows = append(rows, r)
			next := guide
			switch branch {
			case "├ ":
				next += "│ "
			case "└ ":
				next += "  "
			}
			kids := children[n.ID]
			for i, ep := range eps {
				b := "├ "
				if i == len(eps)-1 && len(kids) == 0 {
					b = "└ "
				}
				h := aggregateHealth(checkHealth[ep.ID])
				details := []string{"Endpoint: " + ep.ID, "Node: " + ep.Node, "Address: " + ep.Address, "Network: " + ep.Network, "Execution scope: " + ep.Observer}
				if len(ep.Via) > 0 {
					hops := []string{}
					for _, v := range ep.Via {
						hops = append(hops, nodeNames[v])
					}
					details = append(details, "Reached via: "+strings.Join(append(hops, nodeNames[ep.Node]), " → "), "", "An edge in front answers first: its answer proves the edge; the node behind it counts only a passing response not served from the edge's cache.")
				}
				details = append(details, "", "Health aggregates checks that target this endpoint.", "", ep.Notes)
				rows = append(rows, Row{Kind: "endpoint", ID: ep.ID, Health: h, Title: ep.ID, Meta: ep.Network, Tree: next + b, Cols: []string{ep.Network + via(ep), ep.Address}, Details: details})
			}
			for i, k := range kids {
				b := "├ "
				if i == len(kids)-1 {
					b = "└ "
				}
				walk(k, next, b)
			}
		}
		for _, n := range roots {
			walk(n, "", "")
		}
		for _, n := range s.Topology.Nodes {
			walk(n, "", "") // nodes caught in a parent cycle still get a row
		}
		for _, def := range s.Topology.Links {
			l := find(linkStates, def.ID)
			r := entity("link", l, nodeNames[def.From]+" → "+nodeNames[def.To], def.Transport, []string{def.Notes, "Directed initiation, not proven packet-loss direction."})
			r.Tree = "⇢ "
			r.Cols = []string{def.Transport, ""}
			rows = append(rows, r)
		}
	case tabServers:
		known := map[string]bool{}
		for _, n := range s.Topology.Nodes {
			known[n.ID] = true
		}
		for _, n := range s.Topology.Nodes {
			if !serverNode(n, known) {
				continue
			}
			r := node(n)
			place := ""
			if n.Location != nil {
				place = n.Location.Label
			}
			// Not drawn as a table; they make a card's words searchable.
			r.Cols = []string{n.Group, kindLabel(n), place, n.Notes}
			rows = append(rows, r)
		}
	case tabServices:
		for _, e := range s.Services {
			title := e.ID
			meta := ""
			details := []string{}
			cols := []string{"", ""}
			for _, d := range s.Topology.Services {
				if d.ID == e.ID {
					title = d.Name
					meta = nodeNames[d.Node]
					// The host column reads as the way in: the edges in
					// front, then the host and how the service runs there.
					host := nodeNames[d.Node]
					if d.Runtime != "" {
						host += " · " + d.Runtime
					}
					front := viaNames(s.Topology, d.Endpoints, nodeNames)
					if len(front) > 0 {
						host = strings.Join(front, " ⇢ ") + " ⇢ " + host
					}
					cols = []string{host, strings.Join(d.Endpoints, ", ")}
					details = []string{"", "Hosted on: " + d.Node, "Runtime: " + d.Runtime, "Endpoints: " + strings.Join(d.Endpoints, ", "), "Reached via: " + strings.Join(front, " → "), "Depends on: " + strings.Join(d.DependsOn, ", "), "Runbook (not auto-executed): " + d.Runbook, "", d.Notes}
					break
				}
			}
			r := entity("service", e, title, meta, details)
			r.Cols = cols
			rows = append(rows, r)
		}
	case tabPaths:
		for _, e := range s.Paths {
			title := e.ID
			meta := ""
			details := []string{}
			cols := []string{"", ""}
			for _, d := range s.Topology.Paths {
				if d.ID == e.ID {
					title = d.Name
					meta = d.Observer
					route := []string{}
					for _, n := range d.Nodes {
						route = append(route, nodeNames[n])
					}
					if len(route) == 0 && len(d.Candidates) > 0 {
						route = []string{"candidates: " + strings.Join(d.Candidates, " | ")}
					}
					cols = []string{observerNames[d.Observer], strings.Join(route, " → ")}
					details = []string{"", "Observer: " + d.Observer, "Declared topology:", strings.Join(d.Nodes, " → "), "Segments: " + strings.Join(d.Links, ", "), "", "Proxy transport (wire order):"}
					for j, p := range d.Proxies {
						details = append(details, fmt.Sprintf("%d. %s %s (%s)", j+1, p.Kind, p.Address, p.Node))
					}
					if len(d.Candidates) > 0 {
						details = append(details, "Alternative candidates (NOT serial hops):", strings.Join(d.Candidates, " | "))
					}
					details = append(details, "Opaque provider segment: "+fmt.Sprint(d.Opaque), "Depends on: "+strings.Join(d.DependsOn, ", "), "", d.Notes, "", "A declared path is not request-level path proof.")
					break
				}
			}
			r := entity("path", e, title, meta, details)
			r.Cols = cols
			rows = append(rows, r)
		}
	case tabChecks:
		for _, e := range s.Checks {
			d := checkDefs[e.ID]
			title := d.Name
			if title == "" {
				title = e.ID
			}
			meta := d.Observer + " · " + d.Kind + " · " + e.Freshness
			took := ""
			details := []string{"Check: " + e.ID, "Source: " + e.Observer, "Health: " + e.Health, "Freshness: " + e.Freshness, "Evidence: " + e.Evidence, "", e.Reason, "", "Path: " + d.Path, "Target: " + d.Target, "Endpoint: " + d.Endpoint, "Service: " + d.Service, "Failure / recovery threshold: " + fmt.Sprint(d.FailureThreshold) + " / " + fmt.Sprint(d.RecoveryThreshold)}
			if s.DetailsOmitted {
				details = append(details, "", "Summary diagnostics; full evidence is available in check history.")
			}
			if e.Last != nil {
				v := e.Last
				took = fmt.Sprintf("%d ms", v.DurationMS)
				details = append(details, "", "Last sample: "+v.SampledAt.Local().Format("2006-01-02 15:04:05 MST"), "Sample age: "+age(m.Now, v.SampledAt), "Collector received: "+v.ReceivedAt.Local().Format(time.RFC3339), "Outcome: "+v.Outcome, "Stage: "+v.Stage, "Code: "+v.Code, fmt.Sprintf("Elapsed: %d ms", v.DurationMS), "Path evidence: "+v.PathEvidence)
				if edge := edgeAnswer(v.Facts); edge != "" {
					details = append(details, "Answered by: "+edge+" (response headers, not verified routing)")
				}
				details = append(details, "", v.Detail)
				keys := []string{}
				for k := range v.Facts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					details = append(details, k+": "+v.Facts[k])
				}
			}
			r := Row{Kind: "check", ID: e.ID, Title: title, Health: e.Health, Meta: meta, Cols: []string{observerNames[d.Observer], d.Kind, took, e.Freshness}, Details: details}
			if e.Freshness == "paused" || e.Freshness == "maintenance" {
				r.Note = e.Freshness
			}
			rows = append(rows, r)
		}
	case tabIncidents:
		for _, i := range s.Incidents {
			rows = append(rows, incident(i))
		}
	case tabObservers:
		for _, e := range s.Observers {
			title := e.ID
			host := ""
			details := []string{"Observer: " + e.ID, "State: " + e.Health, "Last evidence: " + age(m.Now, e.LastSeen), "", e.Reason}
			for _, d := range s.Topology.Observers {
				if d.ID == e.ID {
					title = d.Name
					host = nodeNames[d.Node]
					details = append(details, "", "Host: "+d.Node, "Context: "+d.Context, "Expected route (not verified): "+d.RouteExpectation)
					break
				}
			}
			rows = append(rows, Row{Kind: "observer", ID: e.ID, Title: title, Health: e.Health, Meta: age(m.Now, e.LastSeen), Cols: []string{host, age(m.Now, e.LastSeen)}, Details: details})
		}
	}
	return rows
}

// tabColumns heads the list columns of the current tab: the title column
// first, then one heading per Row.Cols entry.
func (m *Model) tabColumns() []string {
	switch m.Tab {
	case tabOverview:
		return []string{"ITEM", "KIND"}
	case tabTopology:
		return []string{"NODE · ENDPOINT · LINK", "GROUP · NETWORK", "KIND · ADDRESS"}
	case tabServers:
		return []string{"SERVER", "GROUP", "KIND", "PLACE", "MEMO"}
	case tabServices:
		return []string{"SERVICE", "HOST", "ENDPOINTS"}
	case tabPaths:
		return []string{"PATH", "OBSERVER", "DECLARED ROUTE"}
	case tabChecks:
		return []string{"CHECK", "OBSERVER", "KIND", "TIME", "FRESHNESS"}
	case tabIncidents:
		return []string{"SCOPE", "SINCE", "CHECKS"}
	case tabObservers:
		return []string{"OBSERVER", "HOST", "LAST SEEN"}
	}
	return nil
}

// via is the " · via …" suffix of an endpoint reached through edges.
func via(ep model.Endpoint) string {
	if len(ep.Via) == 0 {
		return ""
	}
	return " · via " + strings.Join(ep.Via, " → ")
}

func aggregateHealth(hs []string) string {
	if len(hs) == 0 {
		return "unknown"
	}
	up, down := 0, 0
	for _, h := range hs {
		switch h {
		case "up":
			up++
		case "down":
			down++
		}
	}
	switch {
	case up == len(hs):
		return "up"
	case down == len(hs):
		return "down"
	case up+down > 0:
		return "degraded"
	}
	return "unknown"
}
func incidentDetails(i model.Incident) []string {
	return []string{"Scope: " + i.Scope, "Since: " + i.Since.Local().Format(time.RFC3339), "", i.Summary, "", "Failed checks:", strings.Join(i.Checks, ", "), "", "Other-observer response evidence:", strings.Join(i.CounterEvidence, ", "), "", "Hypothesis (not a finding):", i.Hypothesis, "", "No inference about all IP ports, untested UDP, physical power or GFW authority."}
}
func age(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < 0 {
		return "clock ahead"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm ago", int(d.Hours()), int(d.Minutes())%60)
}

func (m *Model) setTab(t int) {
	if t != m.Tab && t == tabAbout {
		m.aboutAt = m.Now
	}
	m.Tab = t
	m.Selected = 0
	m.Offset = 0
	m.Detail = false
}

// Animating reports how often the run loop should repaint without input.
func (m *Model) Animating() time.Duration {
	switch {
	case m.Tab == tabAbout:
		return 70 * time.Millisecond
	case m.Loading != "":
		return 90 * time.Millisecond // the spinner turns
	case m.Tab == tabTopology && !m.Detail:
		return 140 * time.Millisecond
	}
	return 500 * time.Millisecond
}
func (m *Model) phase() float64 { return float64(m.Now.UnixMilli()%3_600_000) / 1000 }

// NextObserver cycles the observer view: all observers, then each declared one.
func (m *Model) NextObserver() {
	views := []string{""}
	for _, o := range m.Snapshot.Topology.Observers {
		views = append(views, o.ID)
	}
	next := ""
	for i, v := range views {
		if v == m.Observer {
			next = views[(i+1)%len(views)]
			break
		}
	}
	m.Observer = next
	m.Selected = 0
	m.Offset = 0
	if m.Edit != nil {
		m.rebuild()
	}
}

// Key returns quit/refresh/view actions; it never performs network calls itself.
func (m *Model) Key(k string) string {
	if strings.HasPrefix(k, "mouse:") {
		k = m.mouse(k)
		if k == "" {
			return ""
		}
	}
	if m.modal != nil {
		return m.modalKey(k)
	}
	if m.Searching {
		switch k {
		case "esc", "enter":
			m.Searching = false
		case "backspace":
			r := []rune(m.Search)
			if len(r) > 0 {
				m.Search = string(r[:len(r)-1])
			}
		case "ctrl-c":
			return "quit"
		default:
			if strings.HasPrefix(k, "paste:") {
				m.Search += secure.Text(strings.TrimPrefix(k, "paste:"))
			} else if !strings.Contains(k, "\x1b") && len([]rune(k)) == 1 {
				m.Search += k
			}
		}
		if len([]rune(m.Search)) > 120 {
			m.Search = string([]rune(m.Search)[:120])
		}
		m.Selected = 0
		m.Offset = 0
		return ""
	}
	switch {
	case strings.HasPrefix(k, "tab:"):
		if n, e := strconv.Atoi(k[4:]); e == nil && n >= 0 && n < len(tabs) {
			m.setTab(n)
		}
		return ""
	case strings.HasPrefix(k, "row:"):
		if n, e := strconv.Atoi(k[4:]); e == nil {
			if n == m.Selected && !m.Detail {
				m.Detail = true
				m.Offset = 0
			} else {
				m.Selected = n
			}
		}
		k = ""
	case strings.HasPrefix(k, "select:"), strings.HasPrefix(k, "goto:"):
		// select: picks a row in this tab when it has one; goto: (and a
		// select: this tab cannot satisfy) opens the entity's own tab.
		parts := strings.SplitN(k, ":", 3)
		found := false
		for i, r := range m.Rows() {
			if len(parts) == 3 && parts[0] == "select" && r.Kind == parts[1] && r.ID == parts[2] {
				m.Selected = i
				found = true
			}
		}
		if !found && len(parts) == 3 {
			m.selectEntity(parts[1], parts[2])
		}
		k = ""
	case strings.HasPrefix(k, "key:"):
		return m.Key(k[4:])
	}
	switch k {
	case "q", "ctrl-c":
		if m.Edit != nil && m.Edit.Dirty && k == "q" {
			m.confirm("Discard unsaved changes?", []string{"The draft has changes that are not written to", m.Edit.Path + ".", "", "Press y to quit without saving, or Esc to go back and ^S to save."}, func() string { return "quit" })
			return ""
		}
		return "quit"
	case "r":
		if m.Edit == nil {
			return "refresh"
		}
	case "v":
		if m.Edit != nil {
			m.NextObserver()
			return ""
		}
		return "view"
	case "/":
		m.Searching = true
	case "?":
		if m.Tab == tabAbout {
			m.setTab(tabOverview)
		} else {
			m.setTab(tabAbout)
		}
	case "m":
		for i, mode := range mapModes {
			if mode == m.mapMode() {
				m.MapMode = mapModes[(i+1)%len(mapModes)]
				break
			}
		}
	case "z":
		m.Zoom = (m.Zoom + 1) % 3
	case "x":
		if m.Preview != nil {
			m.scenarioMenu()
		}
	case "D":
		m.Debug = !m.Debug
	case "a", "e", "d", "delete":
		m.editKey(k)
	case "ctrl-s", "w":
		if m.Edit != nil {
			m.save()
		}
	case "u":
		if m.Edit != nil {
			if m.Edit.Undo() {
				m.rebuild()
				m.say("Undid last change", false)
			} else {
				m.say("Nothing to undo", false)
			}
		}
	case "esc":
		m.Search = ""
		m.Detail = false
	case "enter":
		m.Detail = !m.Detail
		m.Offset = 0
	case "tab", "right", "l":
		m.setTab((m.Tab + 1) % len(tabs))
	case "shift-tab", "left", "h":
		m.setTab((m.Tab + len(tabs) - 1) % len(tabs))
	case "down", "j":
		if m.Detail {
			m.Offset++
		} else {
			m.Selected++
		}
	case "up", "k":
		if m.Detail {
			m.Offset--
		} else {
			m.Selected--
		}
	case "wheel-down", "wheel-up":
		if m.Tab == tabAbout {
			// The wheel turns the globe.
			if k == "wheel-down" {
				m.aboutSpin += .3
			} else {
				m.aboutSpin -= .3
			}
			return ""
		}
		if k == "wheel-up" {
			if m.Detail {
				m.Offset -= 3
			} else {
				m.Selected -= 3
			}
			break
		}
		if m.Detail {
			m.Offset += 3
		} else {
			m.Selected += 3
		}
	case "pgdown":
		if m.Detail {
			m.Offset += 10
		} else {
			m.Selected += 10
		}
	case "pgup":
		if m.Detail {
			m.Offset -= 10
		} else {
			m.Selected -= 10
		}
	case "home", "g":
		m.Selected = 0
		m.Offset = 0
	case "end", "G":
		m.Selected = len(m.Rows()) - 1
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] < byte('1'+len(tabs)) {
			m.setTab(int(k[0] - '1'))
		}
	}
	rows := m.Rows()
	if m.Selected >= len(rows) {
		m.Selected = len(rows) - 1
	}
	if m.Selected < 0 {
		m.Selected = 0
	}
	if m.Offset < 0 {
		m.Offset = 0
	}
	return ""
}

// mouse translates an SGR mouse report into a key using the hit regions of
// the last frame. Only left presses and the wheel are interpreted.
func (m *Model) mouse(k string) string {
	p := strings.Split(k, ":")
	if len(p) != 5 {
		return ""
	}
	b, e1 := strconv.Atoi(p[1])
	x, e2 := strconv.Atoi(p[2])
	y, e3 := strconv.Atoi(p[3])
	if e1 != nil || e2 != nil || e3 != nil || p[4] != "M" {
		return ""
	}
	x, y = x-1, y-1
	switch b &^ (4 | 8 | 16) {
	case 64:
		return "wheel-up"
	case 65:
		return "wheel-down"
	case 0:
		for i := len(m.hits) - 1; i >= 0; i-- {
			h := m.hits[i]
			if m.modal != nil && !h.modal {
				continue
			}
			if y == h.y && x >= h.x && x < h.x+h.w {
				return h.key
			}
		}
	}
	return ""
}

func runeWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200d || r == 0xfe0f {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xfe10 && r <= 0xfe6f || r >= 0xff01 && r <= 0xff60 || r >= 0xffe0 && r <= 0xffe6 || r >= 0x1f300 && r <= 0x1faff || r >= 0x20000 && r <= 0x3fffd) {
		return 2
	}
	return 1
}
func width(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = secure.Text(s)
	var b strings.Builder
	n := 0
	for _, r := range s {
		k := runeWidth(r)
		if n+k > w {
			break
		}
		b.WriteRune(r)
		n += k
	}
	if n < w {
		b.WriteString(strings.Repeat(" ", w-n))
	}
	return b.String()
}

// ellipsis shortens s to at most w columns, marking the cut with "…".
// plural counts things: "1 check", "2 checks".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func ellipsis(s string, w int) string {
	if width(s) <= w {
		return s
	}
	if w <= 1 {
		return strings.TrimRight(fit(s, w), " ")
	}
	return strings.TrimRight(fit(s, w-1), " ") + "…"
}

// wrap breaks at spaces when possible and hard-wraps long words.
func wrap(s string, w int) []string {
	if w < 1 {
		return nil
	}
	s = secure.Text(s)
	out := []string{}
	line := []rune{}
	n := 0
	lastSpace := -1
	for _, r := range s {
		k := runeWidth(r)
		if n+k > w {
			if lastSpace > 0 && r != ' ' {
				rest := append([]rune{}, line[lastSpace+1:]...)
				out = append(out, string(line[:lastSpace]))
				line, n = rest, width(string(rest))
			} else {
				out = append(out, string(line))
				line, n = nil, 0
			}
			lastSpace = -1
			if r == ' ' {
				continue
			}
		}
		if r == ' ' {
			lastSpace = len(line)
		}
		line = append(line, r)
		n += k
	}
	out = append(out, string(line))
	return out
}
