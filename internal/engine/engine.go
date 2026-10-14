// Package engine derives presentation states from immutable observations. It
// never manufactures packet-level evidence or identifies a filtering authority.
package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Evaluate(x model.Check, rows []model.Observation, now time.Time) model.CheckState {
	s := model.CheckState{ID: x.ID, Observer: x.Observer, Health: "unknown", Freshness: "never", Evidence: "unknown", Reason: "not yet measured"}
	if x.Disabled {
		s.Freshness = "paused"
		s.Reason = "check paused"
		return s
	}
	if x.Maintenance {
		s.Freshness = "maintenance"
		s.Reason = "maintenance; excluded from alerts"
		return s
	}
	if len(rows) == 0 {
		return s
	}
	rows = append([]model.Observation(nil), rows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SampledAt.Equal(rows[j].SampledAt) {
			if rows[i].Boot == rows[j].Boot {
				return rows[i].Sequence < rows[j].Sequence
			}
			return rows[i].ID < rows[j].ID
		}
		return rows[i].SampledAt.Before(rows[j].SampledAt)
	})
	confirmedDown := false
	var previous time.Time
	for _, v := range rows {
		if !previous.IsZero() && v.SampledAt.Sub(previous) > time.Duration(x.StaleSeconds)*time.Second {
			s.Failures = 0
			s.Successes = 0
			confirmedDown = false
			s.Health = "unknown"
		}
		previous = v.SampledAt
		last := v
		s.Last = &last
		s.Evidence = v.Evidence
		s.Reason = v.Detail
		switch v.Outcome {
		case "ok":
			s.Successes++
			s.Failures = 0
			if confirmedDown && s.Successes < x.RecoveryThreshold {
				s.Health = "degraded"
				s.Reason = "recovering; waiting for consecutive successes"
			} else {
				s.Health = "up"
				confirmedDown = false
			}
			s.Since = v.SampledAt
		case "fail":
			if s.Failures == 0 {
				s.Since = v.SampledAt
			}
			s.Failures++
			s.Successes = 0
			if s.Failures >= x.FailureThreshold || confirmedDown {
				s.Health = "down"
				confirmedDown = true
			} else {
				s.Health = "degraded"
				s.Reason = "failure observed; confirmation threshold not reached"
			}
		case "policy":
			s.Failures = 0
			s.Successes = 0
			s.Health = "degraded"
			s.Since = v.SampledAt
			s.Reason = "policy violation: " + v.Code
			confirmedDown = false
		default:
			s.Failures = 0
			s.Successes = 0
			s.Health = "unknown"
			s.Since = v.SampledAt
			confirmedDown = false
			if v.Outcome == "error" {
				s.Reason = "monitor-side error: " + v.Code
			}
		}
	}
	s.Freshness = "fresh"
	if s.Last.SampledAt.After(now.Add(5 * time.Second)) {
		s.Health = "unknown"
		s.Freshness = "stale"
		s.Reason = "executor clock is ahead; sample not treated as current"
		s.Failures = 0
		s.Successes = 0
	} else if now.Sub(s.Last.SampledAt) > time.Duration(x.StaleSeconds)*time.Second {
		s.Health = "unknown"
		s.Freshness = "stale"
		s.Reason = "measurement expired; last outcome is historical, not current"
		s.Failures = 0
		s.Successes = 0
	}
	return s
}
func Build(c model.Config, instance, view string, rows []model.Observation, beats map[string]model.Heartbeat, now time.Time) model.Snapshot {
	revision := config.Revision(c)
	out := model.Snapshot{Schema: model.SchemaVersion, Version: model.Version, Project: c.Project, Instance: instance, Revision: revision, GeneratedAt: now.UTC(), ViewObserver: view, Topology: config.Public(c), Checks: []model.CheckState{}, Nodes: []model.EntityState{}, Services: []model.EntityState{}, Paths: []model.EntityState{}, Links: []model.EntityState{}, Observers: []model.ObserverState{}, Incidents: []model.Incident{}}
	checkDefs := map[string]model.Check{}
	for _, x := range c.Checks {
		checkDefs[x.ID] = x
	}
	histories := map[string][]model.Observation{}
	lastSeen := map[string]time.Time{}
	for id, h := range beats {
		if h.Project == c.Project && h.Revision == revision && !h.SampledAt.After(now.Add(5*time.Second)) {
			lastSeen[id] = h.SampledAt
		}
	}
	for _, v := range rows {
		if v.Project != c.Project || v.Revision != revision {
			continue
		}
		x, ok := checkDefs[v.Check]
		if !ok || x.Observer != v.Observer {
			continue
		}
		histories[v.Check] = append(histories[v.Check], v)
		if !v.SampledAt.After(now.Add(5*time.Second)) && v.SampledAt.After(lastSeen[v.Observer]) {
			lastSeen[v.Observer] = v.SampledAt
		}
	}
	allStates := map[string]model.CheckState{}
	checks := map[string]model.Check{}
	for _, x := range c.Checks {
		checks[x.ID] = x
		allStates[x.ID] = Evaluate(x, histories[x.ID], now)
		if view == "" || view == x.Observer {
			out.Checks = append(out.Checks, allStates[x.ID])
		}
	}
	for _, o := range c.Observers {
		s := model.ObserverState{ID: o.ID, Health: "unknown", LastSeen: lastSeen[o.ID], Reason: "no recent executor evidence"}
		if !s.LastSeen.IsZero() && now.Sub(s.LastSeen) <= time.Duration(o.StaleSeconds)*time.Second {
			s.Health = "up"
			s.Reason = "recent executor evidence; target health evaluated separately"
		}
		out.Observers = append(out.Observers, s)
	}
	relevant := func(x model.Check) bool { return view == "" || x.Observer == view }
	group := func(match func(model.Check) bool) []model.CheckState {
		r := []model.CheckState{}
		for _, x := range c.Checks {
			if relevant(x) && match(x) {
				r = append(r, allStates[x.ID])
			}
		}
		return r
	}
	endpoints := map[string]model.Endpoint{}
	for _, e := range c.Endpoints {
		endpoints[e.ID] = e
	}
	for _, n := range c.Nodes {
		states := group(func(x model.Check) bool { return endpoints[x.Endpoint].Node == n.ID && x.Endpoint != "" })
		s := model.EntityState{ID: n.ID, Health: "unknown", Evidence: "unknown", Reason: "no fresh remote-response evidence"}
		failures := 0
		proof := []string{}
		for _, cs := range states {
			s.Checks = append(s.Checks, cs.ID)
			if cs.Freshness != "fresh" || cs.Last == nil {
				continue
			}
			v := cs.Last
			if v.Evidence == "measured" && v.Connectivity && v.Facts["response_proof"] != "" && v.Facts["response_proof"] != "none" {
				proof = append(proof, cs.ID)
			}
			if cs.Health == "down" {
				failures++
			}
		}
		if len(proof) > 0 {
			s.Health = "up"
			s.Evidence = "measured"
			s.Reason = "remote-response evidence: " + strings.Join(proof, ", ")
			s.Affected = failures > 0
		} else if failures > 0 {
			s.Reason = "configured response paths unavailable; physical power state unknown"
			s.Evidence = "inferred"
			s.Affected = true
		}
		if len(states) == 0 {
			s.Reason = "registered asset; no checks in this view"
		}
		out.Nodes = append(out.Nodes, s)
	}
	pathStates := map[string]model.EntityState{}
	pathDefs := map[string]model.Path{}
	for _, p := range c.Paths {
		pathDefs[p.ID] = p
	}
	var evalPath func(string) model.EntityState
	evalPath = func(id string) model.EntityState {
		if s, ok := pathStates[id]; ok {
			return s
		}
		p := pathDefs[id]
		s := aggregate(id, pathEvidenceStates(group(func(x model.Check) bool { return x.Path == id })), "no endpoint-to-endpoint check; segment health unknown")
		if len(p.Candidates) > 0 {
			up, down, unknown := 0, 0, 0
			for _, q := range p.Candidates {
				cs := evalPath(q)
				s.Checks = boundedChecks(s.Checks, cs.Checks)
				switch cs.Health {
				case "up":
					up++
				case "down":
					down++
				default:
					unknown++
				}
			}
			s.Evidence = "inferred"
			switch {
			case up == len(p.Candidates):
				s.Health = "up"
				s.Reason = "all candidates available; active request selection not inferred"
			case up > 0:
				s.Health = "degraded"
				s.Reason = "usable candidate exists, redundancy reduced; actual failover not verified"
			case down == len(p.Candidates):
				s.Health = "down"
				s.Reason = "all configured candidates failed"
			default:
				s.Health = "unknown"
				s.Reason = fmt.Sprintf("no confirmed available candidate (%d unknown/degraded)", unknown)
			}
		}
		for _, dep := range p.DependsOn {
			v := evalPath(dep)
			if v.Health == "down" || v.Affected {
				s.Affected = true
				s.Reason += "; dependency affected: " + dep
			}
		}
		pathStates[id] = s
		return s
	}
	for _, p := range c.Paths {
		if view == "" || p.Observer == view {
			out.Paths = append(out.Paths, evalPath(p.ID))
		}
	}
	for _, l := range c.Links {
		s := aggregate(l.ID, pathEvidenceStates(group(func(x model.Check) bool { return x.Link == l.ID })), "segment not independently measured")
		for _, p := range c.Paths {
			if !relevant(model.Check{Observer: p.Observer}) {
				continue
			}
			for _, id := range p.Links {
				if id == l.ID && evalPath(p.ID).Health == "down" {
					s.Affected = true
				}
			}
		}
		if s.Affected && len(s.Checks) == 0 {
			s.Reason = "on a failing end-to-end path; individual segment remains unknown"
			s.Evidence = "inferred"
		}
		out.Links = append(out.Links, s)
	}
	serviceDefs := map[string]model.Service{}
	serviceStates := map[string]model.EntityState{}
	for _, s := range c.Services {
		serviceDefs[s.ID] = s
	}
	var evalService func(string) model.EntityState
	evalService = func(id string) model.EntityState {
		if s, ok := serviceStates[id]; ok {
			return s
		}
		def := serviceDefs[id]
		s := aggregate(id, group(func(x model.Check) bool { return x.Service == id }), "registered, not monitored")
		for _, dep := range def.DependsOn {
			v := evalService(dep)
			if v.Health == "down" || v.Affected {
				s.Affected = true
				s.Reason += "; dependency affected: " + dep
			}
		}
		for _, x := range c.Checks {
			if x.Service == id && x.Path != "" && relevant(x) {
				p := evalPath(x.Path)
				if p.Health == "down" {
					s.Affected = true
					s.Reason += "; access path unavailable, intrinsic service state not established"
				}
			}
		}
		serviceStates[id] = s
		return s
	}
	for _, s := range c.Services {
		out.Services = append(out.Services, evalService(s.ID))
	}
	// Incidents aggregate only confirmed failures in the same observer/endpoint
	// scope. They do not diagnose an IP-wide ban, GFW, or untested UDP behavior.
	incidents := map[string]*model.Incident{}
	for _, x := range c.Checks {
		if !relevant(x) {
			continue
		}
		s := allStates[x.ID]
		if s.Freshness != "fresh" || s.Last == nil {
			continue
		}
		policy := s.Last.Outcome == "policy"
		if s.Health != "down" && !policy {
			continue
		}
		scope := x.Observer + "/" + x.Endpoint
		if x.Path != "" {
			scope = x.Observer + "/path/" + x.Path
		} else if x.Endpoint != "" {
			scope = x.Observer + "/asset/" + endpoints[x.Endpoint].Node
		} else {
			scope = x.Observer + "/" + x.ID
		}
		if policy {
			scope += "/policy"
		}
		inc := incidents[scope]
		if inc == nil {
			inc = &model.Incident{ID: revision[:12] + "/" + scope, Scope: scope, Summary: "confirmed failure of the listed checks from this observer", Checks: []string{}, CounterEvidence: []string{}, Since: s.Since}
			if policy {
				inc.Summary = "observed policy constraint violation"
			}
			incidents[scope] = inc
		}
		inc.Checks = append(inc.Checks, x.ID)
		if s.Since.Before(inc.Since) {
			inc.Since = s.Since
		}
		if !policy && x.Endpoint != "" {
			node := endpoints[x.Endpoint].Node
			for _, other := range c.Checks {
				cs := allStates[other.ID]
				if other.Observer != x.Observer && other.Endpoint != "" && endpoints[other.Endpoint].Node == node && cs.Freshness == "fresh" && cs.Last != nil && cs.Last.Evidence == "measured" && cs.Last.Connectivity && cs.Last.Facts["response_proof"] != "" && cs.Last.Facts["response_proof"] != "none" {
					inc.CounterEvidence = boundedChecks(inc.CounterEvidence, []string{other.ID})
				}
			}
			if len(inc.CounterEvidence) > 0 {
				inc.Hypothesis = "source/path-specific interference, filtering or routing fault is possible; blocking authority and untested protocols are not established"
			}
		}
	}
	for _, inc := range incidents {
		sort.Strings(inc.Checks)
		sort.Strings(inc.CounterEvidence)
		inc.CounterEvidence = dedup(inc.CounterEvidence)
		out.Incidents = append(out.Incidents, *inc)
	}
	sort.Slice(out.Incidents, func(i, j int) bool { return out.Incidents[i].ID < out.Incidents[j].ID })
	return out
}
func dedup(s []string) []string {
	out := s[:0]
	for _, v := range s {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
func aggregate(id string, states []model.CheckState, empty string) model.EntityState {
	s := model.EntityState{ID: id, Health: "unknown", Evidence: "unknown", Reason: empty}
	if len(states) == 0 {
		return s
	}
	up, down, degraded, unknown := 0, 0, 0, 0
	for _, v := range states {
		s.Checks = append(s.Checks, v.ID)
		if v.Freshness != "fresh" {
			unknown++
			continue
		}
		switch v.Health {
		case "up":
			up++
		case "down":
			down++
		case "degraded":
			degraded++
		default:
			unknown++
		}
	}
	s.Evidence = "inferred"
	switch {
	case up == len(states):
		s.Health = "up"
		s.Reason = "all checks in this view pass"
	case up > 0:
		s.Health = "degraded"
		s.Reason = "some access checks pass; others fail, degrade or lack current evidence"
	case down == len(states):
		s.Health = "down"
		s.Reason = "all access checks in this view have confirmed failures"
	case degraded > 0:
		s.Health = "degraded"
		s.Reason = "confirmation, recovery or policy issue; inspect check evidence"
	default:
		s.Health = "unknown"
		s.Reason = fmt.Sprintf("no current successful check (%d unknown; %d failed)", unknown, down)
	}
	return s
}

// Runtime selection/handshake metadata and a TUN-visible TCP accept are not
// enough to mark an end-to-end access path healthy.
func pathEvidenceStates(states []model.CheckState) []model.CheckState {
	out := make([]model.CheckState, len(states))
	copy(out, states)
	for i := range out {
		s := &out[i]
		if s.Last != nil && s.Last.Evidence != "measured" {
			s.Health = "unknown"
			s.Reason = "runtime telemetry is not an end-to-end probe"
		} else if s.Health == "up" && (s.Last == nil || s.Last.Facts["response_proof"] == "" || s.Last.Facts["response_proof"] == "none") {
			s.Health = "unknown"
			s.Reason = "no remote protocol response proof"
		}
	}
	return out
}

// A shared candidate DAG must not expand the same descendant list repeatedly.
// Summaries carry a bounded sample; the topology and check list retain all IDs.
func boundedChecks(dst, src []string) []string {
	seen := map[string]bool{}
	for _, id := range dst {
		seen[id] = true
	}
	for _, id := range src {
		if len(dst) >= 32 {
			break
		}
		if !seen[id] {
			dst = append(dst, id)
			seen[id] = true
		}
	}
	return dst
}
