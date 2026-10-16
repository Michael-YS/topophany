package view

import (
	"fmt"
	"sort"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Scope is organizational metadata, never a claim about measurement or routing.
type Scope struct{ Application, Environment, Service, Node, Observer, Path string }
type GroupOptions struct {
	Application, Environment, Service, By, Search string
	Attention                                     bool
	Expanded                                      map[string]bool
}

func ApplicationName(c model.Config, id string) string {
	if id == "" {
		return "Ungrouped"
	}
	for _, a := range c.Applications {
		if a.ID == id && a.Name != "" {
			return a.Name
		}
	}
	return id
}
func EntityScope(c model.Config, kind, id string) Scope {
	var scope Scope
	if kind == "check" {
		for _, x := range c.Checks {
			if x.ID == id {
				scope = Scope{Application: x.Application, Environment: x.Environment, Service: x.Service, Observer: x.Observer, Path: x.Path}
				break
			}
		}
		if scope.Service == "" {
			return scope
		}
		id = scope.Service
	}
	for _, s := range c.Services {
		if s.ID == id {
			scope.Application = s.Application
			scope.Environment = s.Environment
			scope.Service = s.ID
			scope.Node = s.Node
			break
		}
	}
	return scope
}

// GroupedList uses the same evidence rows as CLI output. Group summaries describe
// members, not business availability. Environments never share a summary.
func (v View) GroupedList(tab int, o GroupOptions) []Row {
	c := v.Snapshot.Topology
	rows := v.List(tab, "")
	staleChecks := map[string]bool{}
	pausedChecks := map[string]bool{}
	staleServices, pausedServices := map[string]bool{}, map[string]bool{}
	checkServices := map[string]string{}
	for _, x := range c.Checks {
		checkServices[x.ID] = x.Service
	}
	for _, x := range v.Snapshot.Checks {
		staleChecks[x.ID] = x.Freshness == "stale"
		pausedChecks[x.ID] = x.Freshness == "paused" || x.Freshness == "maintenance"
		if staleChecks[x.ID] {
			staleServices[checkServices[x.ID]] = true
		}
		if pausedChecks[x.ID] {
			pausedServices[checkServices[x.ID]] = true
		}
	}
	type bucket struct {
		id, title string
		rows      []Row
	}
	buckets := map[string]*bucket{}
	order := []string{}
	flat := []Row{}
	for _, r := range rows {
		s := EntityScope(c, r.Kind, r.ID)
		if !scopeMatches(o.Application, s.Application) || !scopeMatches(o.Environment, s.Environment) || o.Service != "" && s.Service != o.Service {
			continue
		}
		app := ApplicationName(c, s.Application)
		env := s.Environment
		if env == "" {
			env = "Unspecified"
		}
		key, label := s.Application, app
		switch o.By {
		case "service":
			key = s.Service
			label = s.Service
			for _, d := range c.Services {
				if d.ID == key && d.Name != "" {
					label = d.Name
				}
			}
			if key == "" {
				label = "Infrastructure checks"
			}
		case "host":
			key = s.Node
			label = s.Node
			for _, n := range c.Nodes {
				if n.ID == key && n.Name != "" {
					label = n.Name
				}
			}
			if key == "" {
				label = "Unassigned"
			}
		case "observer":
			key = s.Observer
			label = s.Observer
			for _, d := range c.Observers {
				if d.ID == key && d.Name != "" {
					label = d.Name
				}
			}
			if key == "" {
				label = "Unassigned"
			}
		case "path":
			key = s.Path
			label = s.Path
			if key == "" {
				label = "No declared path"
			}
		}
		if o.Attention && !NeedsAttention(r) && r.Health != "unknown" && r.Note != "outdated" {
			continue
		}
		hay := strings.ToLower(r.ID + " " + r.Title + " " + r.Meta + " " + strings.Join(r.Cols, " ") + " " + app + " " + s.Application + " " + env + " " + label)
		if !strings.Contains(hay, strings.ToLower(o.Search)) {
			continue
		}
		if o.By == "flat" {
			flat = append(flat, r)
			continue
		}
		id := fmt.Sprintf("%d/%s/%s/%s", tab, o.By, key, s.Environment)
		if buckets[id] == nil {
			buckets[id] = &bucket{id: id, title: label + " / " + env}
			order = append(order, id)
		}
		buckets[id].rows = append(buckets[id].rows, r)
	}
	if o.By == "flat" {
		return flat
	}
	sort.SliceStable(order, func(i, j int) bool { return buckets[order[i]].title < buckets[order[j]].title })
	out := []Row{}
	for _, id := range order {
		b := buckets[id]
		problems, unknown, stale, paused := 0, 0, 0, 0
		details := []string{"Member evidence only; not business availability.", "Observer and path failures do not prove an application outage.", ""}
		for _, r := range b.rows {
			if r.Health == "down" || r.Health == "degraded" || r.Note == "affected" {
				problems++
			}
			if r.Health == "unknown" {
				unknown++
			}
			if r.Note == "outdated" || r.Note == "cached" || r.Kind == "check" && staleChecks[r.ID] || r.Kind == "service" && staleServices[r.ID] {
				stale++
			}
			if r.Note == "paused" || r.Note == "maintenance" || r.Kind == "service" && pausedServices[r.ID] {
				paused++
			}
			details = append(details, r.Title+": "+HealthWord(r.Health))
		}
		details = append(v.groupEvidence(b.rows), details...)
		summary := fmt.Sprintf("%d members · %d attention · %d unknown · %d stale · %d paused", len(b.rows), problems, unknown, stale, paused)
		health := "up"
		if unknown > 0 || stale > 0 || paused > 0 {
			health = "unknown"
		}
		if problems > 0 {
			health = "degraded"
		}
		tree := "▶ "
		expanded := o.Expanded[id] || o.Search != "" || o.Attention
		if expanded {
			tree = "▼ "
		}
		compact := fmt.Sprintf("%d members", len(b.rows))
		if problems > 0 {
			compact += fmt.Sprintf(" · %d attention", problems)
		}
		if unknown > 0 {
			compact += fmt.Sprintf(" · %d unknown", unknown)
		}
		if stale > 0 {
			compact += fmt.Sprintf(" · %d stale", stale)
		}
		if paused > 0 {
			compact += fmt.Sprintf(" · %d paused", paused)
		}
		group := Row{Kind: "group", ID: id, Title: b.title, Tree: tree, Health: health, Cols: []string{compact}, Details: append([]string{summary, ""}, details...)}
		if v.Cached {
			group.Health = "unknown"
			group.Note = "cached"
		}
		out = append(out, group)
		if expanded {
			for _, r := range b.rows {
				r.Tree = "  "
				out = append(out, r)
			}
		}
	}
	return out
}

// "-" selects the missing value; it cannot be a configured identifier.
func scopeMatches(filter, value string) bool {
	return filter == "" || filter == value || filter == "-" && value == ""
}
