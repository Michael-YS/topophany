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

var tabs = []string{"Overview", "Topology", "Services", "Paths", "Checks", "Incidents", "Observers", "About"}

const (
	tabOverview = iota
	tabTopology
	tabServices
	tabPaths
	tabChecks
	tabIncidents
	tabObservers
	tabAbout
)

type Row struct {
	Kind, ID, Health, Title, Meta string
	Details                       []string
}
type Model struct {
	Snapshot              model.Snapshot
	Server                string
	Error                 string
	Observer              string
	Tab, Selected, Offset int
	Width, Height         int
	Search                string
	Searching             bool
	Detail                bool
	NoColor               bool
	Colors                int // colorTrue or color256
	Now                   time.Time
	MapMode               string // "", "geo" or "network"; "" picks geo when locations exist
	Zoom                  int
	Edit                  *Editor // non-nil only for topo edit
	Preview               Preview // non-nil only for topo preview
	Debug                 bool    // performance/geometry overlay (D)
	Transparent           bool    // keep the terminal background (TOPO_BACKGROUND=terminal)
	perf                  perf
	modal                 *modal
	notice                string
	noticeErr             bool
	noticeAt              time.Time
	aboutAt               time.Time
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

func (m *Model) Rows() []Row {
	s := m.Snapshot
	rows := []Row{}
	nodeNames := map[string]string{}
	for _, n := range s.Topology.Nodes {
		nodeNames[n.ID] = n.Name
	}
	checkDefs := map[string]model.Check{}
	for _, c := range s.Topology.Checks {
		checkDefs[c.ID] = c
	}
	entity := func(kind string, e model.EntityState, title, meta string, details []string) Row {
		return Row{Kind: kind, ID: e.ID, Health: e.Health, Title: title, Meta: meta, Details: append([]string{"ID: " + e.ID, "State: " + e.Health, "Evidence: " + e.Evidence, "Affected dependency: " + fmt.Sprint(e.Affected), "", e.Reason, "Checks: " + strings.Join(e.Checks, ", ")}, details...)}
	}
	find := func(list []model.EntityState, id string) model.EntityState {
		for _, e := range list {
			if e.ID == id {
				return e
			}
		}
		return model.EntityState{ID: id, Health: "unknown", Evidence: "unknown", Reason: "no state in this view"}
	}
	switch m.Tab {
	case tabOverview:
		for _, i := range s.Incidents {
			rows = append(rows, Row{Kind: "incident", ID: i.ID, Health: "down", Title: i.Scope, Meta: i.Summary, Details: incidentDetails(i)})
		}
		for _, p := range s.Paths {
			rows = append(rows, entity("path", p, "Path / "+p.ID, "", nil))
		}
		if len(rows) == 0 {
			for _, v := range s.Services {
				rows = append(rows, entity("service", v, "Service / "+v.ID, "", nil))
			}
		}
	case tabTopology:
		checkHealth := map[string][]string{}
		for _, cs := range s.Checks {
			if d, ok := checkDefs[cs.ID]; ok && d.Endpoint != "" {
				checkHealth[d.Endpoint] = append(checkHealth[d.Endpoint], cs.Health)
			}
		}
		for _, n := range s.Topology.Nodes {
			e := find(s.Nodes, n.ID)
			title := n.Name
			if title == "" {
				title = n.ID
			}
			if n.Parent != "" {
				title = "└─ " + title
			}
			place := "Location: not declared"
			if l := n.Location; l != nil {
				place = fmt.Sprintf("Location: %s (%.2f, %.2f) — declared, not inferred from IP", l.Label, l.Lat, l.Lon)
			}
			details := []string{"", "Node identity: " + n.ID, "Group: " + n.Group, "Kind: " + n.Kind, "Parent: " + n.Parent, place, "", n.Notes, "", "Access endpoints:"}
			eps := []model.Endpoint{}
			for _, ep := range s.Topology.Endpoints {
				if ep.Node == n.ID {
					details = append(details, ep.ID+" ["+ep.Network+"] "+ep.Address)
					eps = append(eps, ep)
				}
			}
			rows = append(rows, entity("node", e, title, n.Group, details))
			for _, ep := range eps {
				h := aggregateHealth(checkHealth[ep.ID])
				indent := "   "
				if n.Parent != "" {
					indent = "      "
				}
				rows = append(rows, Row{Kind: "endpoint", ID: ep.ID, Health: h, Title: indent + "⌁ " + ep.ID, Meta: ep.Network, Details: []string{"Endpoint: " + ep.ID, "Node: " + ep.Node, "Address: " + ep.Address, "Network: " + ep.Network, "Execution scope: " + ep.Observer, "", "Health aggregates checks that target this endpoint.", "", ep.Notes}})
			}
		}
		for _, def := range s.Topology.Links {
			l := find(s.Links, def.ID)
			rows = append(rows, entity("link", l, "↳ "+nodeNames[def.From]+" → "+nodeNames[def.To], def.Transport, []string{def.Notes, "Directed initiation, not proven packet-loss direction."}))
		}
	case tabServices:
		for _, e := range s.Services {
			title := e.ID
			meta := ""
			details := []string{}
			for _, d := range s.Topology.Services {
				if d.ID == e.ID {
					title = d.Name
					meta = nodeNames[d.Node]
					details = []string{"", "Hosted on: " + d.Node, "Endpoints: " + strings.Join(d.Endpoints, ", "), "Depends on: " + strings.Join(d.DependsOn, ", "), "Runbook (not auto-executed): " + d.Runbook, "", d.Notes}
					break
				}
			}
			rows = append(rows, entity("service", e, title, meta, details))
		}
	case tabPaths:
		for _, e := range s.Paths {
			title := e.ID
			meta := ""
			details := []string{}
			for _, d := range s.Topology.Paths {
				if d.ID == e.ID {
					title = d.Name
					meta = d.Observer
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
			rows = append(rows, entity("path", e, title, meta, details))
		}
	case tabChecks:
		for _, e := range s.Checks {
			d := checkDefs[e.ID]
			title := d.Name
			if title == "" {
				title = e.ID
			}
			meta := d.Observer + " · " + d.Kind + " · " + e.Freshness
			details := []string{"Check: " + e.ID, "Source: " + e.Observer, "Health: " + e.Health, "Freshness: " + e.Freshness, "Evidence: " + e.Evidence, "", e.Reason, "", "Path: " + d.Path, "Target: " + d.Target, "Endpoint: " + d.Endpoint, "Service: " + d.Service, "Failure / recovery threshold: " + fmt.Sprint(d.FailureThreshold) + " / " + fmt.Sprint(d.RecoveryThreshold)}
			if e.Last != nil {
				v := e.Last
				details = append(details, "", "Last sample: "+v.SampledAt.Local().Format("2006-01-02 15:04:05 MST"), "Sample age: "+age(m.Now, v.SampledAt), "Collector received: "+v.ReceivedAt.Local().Format(time.RFC3339), "Outcome: "+v.Outcome, "Stage: "+v.Stage, "Code: "+v.Code, fmt.Sprintf("Elapsed: %d ms", v.DurationMS), "Path evidence: "+v.PathEvidence, "", v.Detail)
				keys := []string{}
				for k := range v.Facts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					details = append(details, k+": "+v.Facts[k])
				}
			}
			rows = append(rows, Row{Kind: "check", ID: e.ID, Title: title, Health: e.Health, Meta: meta, Details: details})
		}
	case tabIncidents:
		for _, i := range s.Incidents {
			rows = append(rows, Row{Kind: "incident", ID: i.ID, Health: "down", Title: i.Scope, Meta: i.Summary, Details: incidentDetails(i)})
		}
	case tabObservers:
		for _, e := range s.Observers {
			title := e.ID
			details := []string{"Observer: " + e.ID, "State: " + e.Health, "Last evidence: " + age(m.Now, e.LastSeen), "", e.Reason}
			for _, d := range s.Topology.Observers {
				if d.ID == e.ID {
					title = d.Name
					details = append(details, "", "Host: "+d.Node, "Context: "+d.Context, "Expected route (not verified): "+d.RouteExpectation)
					break
				}
			}
			rows = append(rows, Row{Kind: "observer", ID: e.ID, Title: title, Health: e.Health, Meta: age(m.Now, e.LastSeen), Details: details})
		}
	}
	filtered := rows[:0]
	search := strings.ToLower(m.Search)
	for _, r := range rows {
		if search == "" || strings.Contains(strings.ToLower(r.ID+" "+r.Title+" "+r.Meta), search) {
			if m.Cached() {
				r.Health = "unknown"
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
	case strings.HasPrefix(k, "select:"):
		parts := strings.SplitN(k, ":", 3)
		for i, r := range m.Rows() {
			if len(parts) == 3 && r.Kind == parts[1] && r.ID == parts[2] {
				m.Selected = i
			}
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
		if m.mapMode() == "geo" {
			m.MapMode = "network"
		} else {
			m.MapMode = "geo"
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
	case "wheel-down":
		if m.Detail {
			m.Offset += 3
		} else {
			m.Selected += 3
		}
	case "wheel-up":
		if m.Detail {
			m.Offset -= 3
		} else {
			m.Selected -= 3
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
