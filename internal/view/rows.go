package view

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Rows are a tab's entries, unfiltered and unmarked.
func (v View) Rows(tab int) []Row {
	s := v.Snapshot
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
		r.Health, r.Note = entityPresentation(e)
		return r
	}
	index := func(list []model.EntityState) map[string]model.EntityState {
		out := make(map[string]model.EntityState, len(list))
		for _, e := range list {
			out[e.ID] = e
		}
		return out
	}
	nodeStates, endpointStates, linkStates := index(s.Nodes), index(s.Endpoints), index(s.Links)
	resources := map[string][]CardResource{}
	if tab == Servers || tab == Topology {
		resources = v.HostResources()
	}
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
		details := []string{"", "Node identity: " + n.ID, "Group: " + n.Group, "Kind: " + n.Kind, "Provider: " + ProviderName(n.Provider), "Parent: " + n.Parent, place, "", n.Notes}
		for _, r := range resources[n.ID] {
			details = append(details, "", "Local resources: "+r.Observer.Name+" · "+r.Health+" · "+r.Freshness, "Resource check: "+r.Check, "Sampled: "+Stamp(v.Now, r.SampledAt))
			for _, pair := range ResourceValues(r) {
				details = append(details, pair[0]+": "+pair[1])
			}
		}
		details = append(details, "", "Access endpoints:")
		fronts := []string{}
		for _, ep := range s.Topology.Endpoints {
			if ep.Node == n.ID {
				details = append(details, ep.ID+" ["+ep.Network+"] "+ep.Address+via(ep))
			}
			if slices.Contains(ep.Via, n.ID) {
				fronts = append(fronts, ep.ID+" → "+nodeNames[ep.Node])
			}
		}
		if len(fronts) > 0 {
			details = append(details, "", "Answers first for:")
			details = append(details, fronts...)
		}
		return entity("node", find(nodeStates, n.ID), nodeNames[n.ID], n.Group, details)
	}
	// The ledger's record of an open incident, and of those it remembers.
	opened := map[string]*model.IncidentRecord{}
	current := map[string]bool{}
	for _, i := range s.Incidents {
		current[i.ID] = true
	}
	for k, r := range s.Ledger {
		if r.State == "open" {
			opened[r.ID] = &s.Ledger[k]
		}
	}
	incident := func(i model.Incident) Row {
		if r := opened[i.ID]; r != nil && r.Since.Before(i.Since) {
			i.Since = r.Since
		}
		return Row{Kind: "incident", ID: i.ID, Health: "down", Title: v.IncidentTitle(i), Meta: i.Summary, Cols: []string{Age(v.Now, i.Since), Plural(len(i.Checks), "check")}, Details: IncidentDetails(v.Now, i, opened[i.ID])}
	}
	// remembered lists the incidents that are recovering and, unless only
	// those are wanted, those that closed, newest first. A closed record's
	// ID counts its occurrences, as ID#3 for the third.
	remembered := func(recovering bool) []Row {
		out := []Row{}
		for _, r := range s.Ledger {
			row := Row{Kind: "incident", ID: r.ID, Title: v.IncidentTitle(model.Incident{Scope: r.Scope}), Meta: r.Summary, Cols: []string{Age(v.Now, r.Since), Plural(len(r.Checks), "check")}, Details: RecordDetails(v.Now, r)}
			switch {
			case r.State == "recovering":
				row.Health, row.Note = "degraded", "recovering"
			case r.State == "closed" && !recovering:
				row.ID += "#" + itoa(r.Previous+1)
				row.Health, row.Note = "unknown", "closed"
				if r.Ended == "recovered" {
					row.Health = "up"
				}
				row.Meta = "closed · " + r.Ended
				row.Cols[0] = "ended " + Age(v.Now, r.Closed)
			default:
				continue
			}
			out = append(out, row)
		}
		return out
	}
	switch tab {
	case Overview:
		// Everything that needs a look, worst first: incidents, failures,
		// partial failures, silent observers, then whatever sits on a
		// failing path. Nothing here means nothing needs attention.
		for _, i := range s.Incidents {
			r := incident(i)
			r.Cols = []string{"incident"}
			rows = append(rows, r)
		}
		for _, r := range remembered(true) {
			r.Cols = []string{"incident"}
			rows = append(rows, r)
		}
		// A monitor error or missing evidence can have no service or path
		// to draw attention to it. Keep the check itself visible.
		for _, r := range v.Rows(Checks) {
			if r.Health == "unknown" && NeedsAttention(r) {
				r.Cols = []string{"check"}
				rows = append(rows, r)
			}
		}
		kinds := map[int]string{Services: "service", Paths: "path", Topology: "node", Observers: "observer"}
		for _, t := range []int{Services, Paths, Topology, Observers} {
			if t == Observers && v.Draft {
				continue
			}
			for _, r := range v.Rows(t) {
				if r.Kind == kinds[t] && NeedsAttention(r) {
					r.Tree = ""
					r.Cols = []string{r.Kind}
					rows = append(rows, r)
				}
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return AttentionRank(rows[i]) < AttentionRank(rows[j]) })
	case Topology:
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
			r.Cols = []string{n.Group, KindLabel(n)}
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
				details := []string{"", "Node: " + ep.Node, "Address: " + ep.Address, "Network: " + ep.Network, "Execution scope: " + ep.Observer}
				if len(ep.Via) > 0 {
					hops := []string{}
					for _, v := range ep.Via {
						hops = append(hops, nodeNames[v])
					}
					details = append(details, "Reached via: "+strings.Join(append(hops, nodeNames[ep.Node]), " → "), "", "An edge in front answers first: its answer proves the edge alone, whatever it says of where it came from; the node behind it counts only a check that does not go through the edge.")
				}
				details = append(details, "", "Health aggregates checks that target this endpoint.", "", ep.Notes)
				r := entity("endpoint", find(endpointStates, ep.ID), ep.ID, ep.Network, details)
				r.Tree, r.Cols = next+b, []string{ep.Network + via(ep), ep.Address}
				rows = append(rows, r)
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
	case Servers:
		known := map[string]bool{}
		for _, n := range s.Topology.Nodes {
			known[n.ID] = true
		}
		for _, n := range s.Topology.Nodes {
			if !ServerNode(n, known) {
				continue
			}
			r := node(n)
			place := ""
			if n.Location != nil {
				place = n.Location.Label
			}
			// Not drawn as a table; they make a card's words searchable.
			r.Cols = []string{n.Group, KindLabel(n), place, n.Notes}
			rows = append(rows, r)
		}
	case Services:
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
	case Paths:
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
	case Checks:
		for _, e := range s.Checks {
			d := checkDefs[e.ID]
			title := d.Name
			if title == "" {
				title = e.ID
			}
			meta := d.Observer + " · " + d.Kind + " · " + e.Freshness
			took := ""
			details := []string{"Check: " + e.ID, "Source: " + e.Observer, "Health: " + e.Health, "Freshness: " + e.Freshness, "Evidence: " + e.Evidence, "Contract: " + e.Contract[:min(12, len(e.Contract))], "", e.Reason, ""}
			details = append(details, adapterDetails(d, e)...)
			if fared := AvailabilityDetails(e.Availability); fared != nil {
				details = append(append(details, fared...), "")
			}
			details = append(details, "Path: "+d.Path, "Target: "+d.Target, "Endpoint: "+d.Endpoint, "Service: "+d.Service, "Failure / recovery threshold: "+fmt.Sprint(d.FailureThreshold)+" / "+fmt.Sprint(d.RecoveryThreshold), "Policy confirmation samples: "+fmt.Sprint(max(d.PolicyThreshold, 1)))
			if s.DetailsOmitted {
				details = append(details, "", "Summary diagnostics; full evidence is available in check history.")
			}
			if e.Last != nil {
				o := e.Last
				took = fmt.Sprintf("%d ms", o.DurationMS)
				details = append(details, "", "Last sample: "+o.SampledAt.Local().Format(StampLayout), "Sample age: "+Age(v.Now, o.SampledAt), "Collector received: "+o.ReceivedAt.Local().Format(StampLayout), "Outcome: "+o.Outcome, "Stage: "+o.Stage, "Code: "+o.Code, fmt.Sprintf("Elapsed: %d ms", o.DurationMS), "Path evidence: "+o.PathEvidence)
				if edge := edgeAnswer(o.Facts); edge != "" {
					details = append(details, "Answered by: "+edge+" (response headers, not verified routing)")
				}
				details = append(details, "", o.Detail)
				keys := []string{}
				for k := range o.Facts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					details = append(details, k+": "+o.Facts[k])
				}
			}
			r := Row{Kind: "check", ID: e.ID, Title: title, Health: e.Health, Meta: meta, Cols: []string{observerNames[d.Observer], d.Kind, took, e.Freshness, Up24h(e.Availability)}, Details: details}
			if e.Freshness == "paused" || e.Freshness == "maintenance" {
				r.Note = e.Freshness
			}
			rows = append(rows, r)
		}
	case Incidents:
		for _, i := range s.Incidents {
			rows = append(rows, incident(i))
		}
		rows = append(rows, remembered(false)...)
	case Observers:
		for _, e := range s.Observers {
			title := e.ID
			host := ""
			topology := map[string]string{"current": "current — it measures what the collector counts", "outdated": "outdated — samples of checks that changed are refused until it reloads", "": "not reported"}[e.Contracts]
			details := []string{"Observer: " + e.ID, "State: " + e.Health, "Last evidence: " + Age(v.Now, e.LastSeen), "Topology: " + topology, "", e.Reason}
			for _, d := range s.Topology.Observers {
				if d.ID == e.ID {
					title = d.Name
					host = nodeNames[d.Node]
					details = append(details, "", "Host: "+d.Node, "Context: "+d.Context, "Expected route (not verified): "+d.RouteExpectation)
					break
				}
			}
			r := Row{Kind: "observer", ID: e.ID, Title: title, Health: e.Health, Meta: Age(v.Now, e.LastSeen), Cols: []string{host, Age(v.Now, e.LastSeen)}, Details: details}
			for _, d := range e.Delivery {
				r.Details = append(r.Details, "", "Worker-reported delivery: "+d.Collector, "Reported: "+Age(v.Now, d.ReportedAt), fmt.Sprintf("Pending: %d records / %d bytes; worker quarantine: %d bytes", d.PendingRecords, d.PendingBytes, d.QuarantineBytes), "Last sample ACK: "+Age(v.Now, d.LastAck), "Last contact: "+Age(v.Now, d.LastContact), "Retry: "+Stamp(v.Now, d.RetryAt), "Reason: "+d.Code)
				if (d.Code != "" || d.QuarantineBytes > 0) && !v.Cached && e.Health == "up" {
					r.Note = "delivery attention"
				}
			}
			if s.Storage != nil {
				for _, u := range s.Storage.Observers {
					if u.Observer != e.ID {
						continue
					}
					r.Details = append(r.Details, "", fmt.Sprintf("Storage: %.1f / %.1f MiB", float64(u.Bytes)/(1<<20), float64(u.ByteLimit)/(1<<20)), fmt.Sprintf("Receipts: %d / %d; last hour: %d; projected: %d", u.Receipts, u.ReceiptLimit, u.LastHour, u.Settles))
					if u.Fills {
						r.Details = append(r.Details, "Projected quota exhaustion: "+u.FullIn.Round(time.Second).String())
						if !v.Cached {
							r.Note = "storage pressure"
						}
					}
				}
			}
			if e.Health == "up" && e.Contracts == "outdated" {
				r.Note = "outdated"
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// IncidentTitle names an incident by who saw it and what failed, e.g.
// "长沙 Windows → 主链 DMIT → AAITR 家宽" for scope cs-win/path/w-dmit-aaitr.
func (v View) IncidentTitle(i model.Incident) string {
	t := v.Snapshot.Topology
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

// IncidentDetails is the evidence behind an incident, with what the
// collector's ledger remembers of it when r is its record. A collector
// keeps a bounded tail of each check's samples, so the start of a long
// incident is the earliest the ledger saw, not what the tail still shows.
func IncidentDetails(now time.Time, i model.Incident, r *model.IncidentRecord) []string {
	out := []string{"Scope: " + i.Scope, "Since: " + Stamp(now, i.Since)}
	if r != nil {
		out = append(out, "Opened: "+Stamp(now, r.Opened), "Seen before: "+before(r.Previous))
	}
	return append(out, "", i.Summary, "", "Failed checks:", strings.Join(i.Checks, ", "), "", "Other-observer response evidence:", strings.Join(i.CounterEvidence, ", "), "", "Hypothesis (not a finding):", i.Hypothesis, "", "No inference about all IP ports, untested UDP, physical power or GFW authority.")
}

// endings say what each way a record closed means.
var endings = map[string]string{
	"recovered":   "every check that failed passes again",
	"unconfirmed": "its evidence lapsed or no longer confirms the failure; whether it recovered is not established",
	"paused":      "its checks were paused or put in maintenance",
	"changed":     "what its checks measure changed, or they were removed",
}

// Ending says what a way a record closed means: "recovered" is "every
// check that failed passes again".
func Ending(ended string) string { return endings[ended] }

// RecordDetails is what the collector's ledger remembers of an incident
// that is recovering or closed.
func RecordDetails(now time.Time, r model.IncidentRecord) []string {
	out := []string{"Scope: " + r.Scope, "Since: " + Stamp(now, r.Since), "Opened: " + Stamp(now, r.Opened)}
	switch r.State {
	case "recovering":
		out = append(out, "Recovering: the checks that failed pass again, short of their recovery threshold")
	case "closed":
		out = append(out, "Closed: "+Stamp(now, r.Closed), "Lasted: "+Duration(r.Closed.Sub(r.Since)), "Ended: "+r.Ended+" — "+endings[r.Ended])
	}
	return append(out, "Seen before: "+before(r.Previous), "", r.Summary, "", "Failed checks:", strings.Join(r.Checks, ", "), "", "Remembered by the collector's ledger, not current evidence.")
}

// before says how often an incident was seen before: "never", "once", "3 times".
func before(n int) string {
	switch n {
	case 0:
		return "never"
	case 1:
		return "once"
	}
	return itoa(n) + " times"
}

// via is the " · via …" suffix of an endpoint reached through edges.
func via(ep model.Endpoint) string {
	if len(ep.Via) == 0 {
		return ""
	}
	return " · via " + strings.Join(ep.Via, " → ")
}

// StampLayout writes the moments in evidence in local time, to the second.
const StampLayout = "2006-01-02 15:04:05 MST"

// Stamp is a moment and how long ago it was: "2026-10-13 10:58:07 PDT ·
// 2m ago".
func Stamp(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format(StampLayout) + " · " + Age(now, t)
}

// Age is how long ago t was: "12s ago", "3m ago", "1h 5m ago".
func Age(now, t time.Time) string {
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

// Plural counts things: "1 check", "2 checks".
func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func itoa(n int) string { return strconv.Itoa(n) }

// entityPresentation is the health and note shared by entity rows and cards.
func entityPresentation(e model.EntityState) (health, note string) {
	if e.Affected && e.Health != "down" {
		note = "affected"
	}
	return e.Health, note
}
