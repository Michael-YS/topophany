package view

import (
	"fmt"
	"sort"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// groupEvidence counts each measurement once, including when services share a
// dependency. Dependencies are references, never extra owned group members.
func (v View) groupEvidence(rows []Row) []string {
	services := map[string]bool{}
	checks := map[string]bool{}
	for _, r := range rows {
		if r.Kind == "service" {
			services[r.ID] = true
		} else if r.Kind == "check" {
			checks[r.ID] = true
		}
	}
	for _, x := range v.Snapshot.Topology.Checks {
		if services[x.Service] {
			checks[x.ID] = true
		}
	}
	total, failed, unknown, stale, paused := 0, 0, 0, 0, 0
	for _, x := range v.Snapshot.Checks {
		if !checks[x.ID] {
			continue
		}
		total++
		if v.Cached {
			unknown++
			stale++
			continue
		}
		if x.Health == "down" || x.Health == "degraded" {
			failed++
		}
		if x.Health == "unknown" {
			unknown++
		}
		if x.Freshness == "stale" {
			stale++
		}
		if x.Freshness == "paused" || x.Freshness == "maintenance" {
			paused++
		}
	}
	out := []string{fmt.Sprintf("Checks: %d total · %d failing · %d unknown · %d stale · %d paused", total, failed, unknown, stale, paused)}
	defs := map[string]model.Service{}
	states := map[string]string{}
	for _, s := range v.Snapshot.Topology.Services {
		defs[s.ID] = s
	}
	for _, s := range v.Snapshot.Services {
		states[s.ID] = s.Health
	}
	seen := map[string]bool{}
	deps := []string{}
	for id := range services {
		s := defs[id]
		for _, dep := range s.DependsOn {
			d, ok := defs[dep]
			if !ok || d.Application == s.Application || seen[dep] {
				continue
			}
			seen[dep] = true
			health := states[dep]
			if health == "" || v.Cached {
				health = "unknown"
			}
			deps = append(deps, fmt.Sprintf("Shared dependency: %s (%s) · %s / %s · %s", d.Name, d.ID, ApplicationName(v.Snapshot.Topology, d.Application), d.Environment, health))
		}
	}
	sort.Strings(deps)
	return append(out, deps...)
}
