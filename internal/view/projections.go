package view

import (
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// ObserverView scopes the verdict and counts to one observer's evidence.
// Verdict needs the checks and current incidents, not another evaluation of
// the samples, which would lose their confirmed threshold state.
func (v View) ObserverView(id string) View {
	v.Snapshot.Checks = slices.DeleteFunc(slices.Clone(v.Snapshot.Checks), func(c model.CheckState) bool { return c.Observer != id })
	v.Snapshot.Incidents = slices.DeleteFunc(slices.Clone(v.Snapshot.Incidents), func(i model.Incident) bool { return !strings.HasPrefix(i.Scope, id+"/") })
	v.Snapshot.ViewObserver = id
	return v
}

// GroupCounts counts each measurement of the group's owned members once.
// Shared dependencies remain references, as in the group's evidence text.
func (v View) GroupCounts(rows []Row) Counts {
	checks, services := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		if r.Kind == "service" {
			services[r.ID] = true
		}
		if r.Kind == "check" {
			checks[r.ID] = true
		}
	}
	for _, x := range v.Snapshot.Topology.Checks {
		if services[x.Service] {
			checks[x.ID] = true
		}
	}
	scoped := v
	scoped.Snapshot.Checks = nil
	for _, x := range v.Snapshot.Checks {
		if checks[x.ID] {
			scoped.Snapshot.Checks = append(scoped.Snapshot.Checks, x)
		}
	}
	return scoped.Counts()
}

// ApplicationGroup carries the same member rows and health as the Services
// tab's application groups, ordered by their first service in the topology.
type ApplicationGroup struct {
	ID, Name, Environment, Health string
	Rows                          []Row
	Counts                        Counts
}

func (v View) ApplicationGroups() []ApplicationGroup {
	type group struct {
		row     Row
		members []Row
	}
	groups := map[string]*group{}
	byService := map[string]string{}
	key := ""
	for _, r := range v.GroupedList(Services, GroupOptions{}) {
		if r.Kind == "group" {
			key = r.ID
			groups[key] = &group{row: r}
			continue
		}
		if r.Kind == "service" {
			groups[key].members = append(groups[key].members, r)
			byService[r.ID] = key
		}
	}
	order := map[string]int{}
	for i, s := range v.Snapshot.Topology.Services {
		order[s.ID] = i
	}
	out := []ApplicationGroup{}
	seen := map[string]bool{}
	for _, s := range v.Snapshot.Topology.Services {
		key := byService[s.ID]
		g := groups[key]
		if g == nil || seen[key] {
			continue
		}
		seen[key] = true
		slices.SortStableFunc(g.members, func(a, b Row) int { return order[a.ID] - order[b.ID] })
		out = append(out, ApplicationGroup{ID: s.Application, Name: ApplicationName(v.Snapshot.Topology, s.Application), Environment: s.Environment, Health: g.row.Health, Rows: g.members, Counts: v.GroupCounts(g.members)})
	}
	return out
}
