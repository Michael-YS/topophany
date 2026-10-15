// Package engine derives presentation states from immutable observations. It
// never manufactures packet-level evidence or identifies a filtering authority.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Evaluate(x model.Check, rows []model.Observation, now time.Time) model.CheckState {
	return EvaluateCarried(x, nil, rows, now)
}

// EvaluateCarried judges x from rows after those folded into carry, as
// Evaluate would from all of them; rows that carry holds are passed over.
func EvaluateCarried(x model.Check, carry *Carry, rows []model.Observation, now time.Time) model.CheckState {
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
	var k Carry
	if carry != nil {
		k = *carry
	}
	rows = slices.DeleteFunc(slices.Clone(rows), k.Holds)
	if len(rows) == 0 {
		return s
	}
	sort.Slice(rows, func(i, j int) bool {
		a := rows[i]
		return earlier(a.SampledAt, a.Boot, a.Sequence, a.ID, rows[j])
	})
	for _, v := range rows {
		k.Fold(v, time.Duration(x.StaleSeconds)*time.Second)
	}
	last := rows[len(rows)-1]
	s.Last = &last
	s.Evidence = last.Evidence
	s.Reason = last.Detail
	s.Failures, s.Successes, s.Since = k.Failures, k.Successes, k.Since
	down := k.down(x.FailureThreshold, x.RecoveryThreshold)
	switch last.Outcome {
	case "ok":
		s.Health = "up"
		if down {
			s.Health = "degraded"
			s.Reason = "recovering; waiting for consecutive successes"
		}
	case "fail":
		s.Health = "down"
		if !down {
			s.Health = "degraded"
			s.Reason = "failure observed; confirmation threshold not reached"
		}
	case "policy":
		s.Health = "degraded"
		s.Reason = "policy violation: " + last.Code
	default:
		if last.Outcome == "error" {
			s.Reason = "monitor-side error: " + last.Code
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

// Build derives the snapshot of c at now from stored observations and
// heartbeats. A sample counts when it carries its check's contract in c
// (or, from a schema 1 executor, c's revision). The snapshot shows only
// public revisions and contracts.
func Build(c model.Config, instance, view string, rows []model.Observation, beats map[string]model.Heartbeat, now time.Time) model.Snapshot {
	return BuildCarried(c, instance, view, rows, nil, beats, now)
}

// BuildCarried is Build where carries hold what samples no longer stored
// left behind: each check is judged from its carry under its contract in
// c, if it has one, and the samples after it.
func BuildCarried(c model.Config, instance, view string, rows []model.Observation, carries []Carry, beats map[string]model.Heartbeat, now time.Time) model.Snapshot {
	revision := config.Revision(c)
	contracts, public := config.Contracts(c), config.PublicContracts(c)
	out := model.Snapshot{Schema: model.SnapshotSchema, Version: model.Version, Project: c.Project, Instance: instance, Revision: config.PublicRevision(c), GeneratedAt: now.UTC(), ViewObserver: view, Topology: config.Public(c), Checks: []model.CheckState{}, Nodes: []model.EntityState{}, Endpoints: []model.EntityState{}, Services: []model.EntityState{}, Paths: []model.EntityState{}, Links: []model.EntityState{}, Observers: []model.ObserverState{}, Incidents: []model.Incident{}}
	checkDefs := map[string]model.Check{}
	for _, x := range c.Checks {
		checkDefs[x.ID] = x
	}
	histories := map[string][]model.Observation{}
	lastSeen := map[string]time.Time{}
	// An executor is alive whatever topology it runs; whether its samples
	// count is decided sample by sample.
	reported := map[string]string{}
	for id, h := range beats {
		if h.Project != c.Project {
			continue
		}
		if !h.SampledAt.After(now.Add(5 * time.Second)) {
			lastSeen[id] = h.SampledAt
		}
		// What it measures: a schema 2 executor digests its contracts; a
		// schema 1 one counts only under the very revision it runs.
		switch {
		case h.Contracts != "":
			reported[id] = map[bool]string{true: "current", false: "outdated"}[h.Contracts == config.ObserverContracts(c, id)]
		case h.Revision != "":
			reported[id] = map[bool]string{true: "current", false: "outdated"}[h.Revision == revision]
		}
	}
	for _, v := range rows {
		if v.Project != c.Project || !config.Counted(v, contracts, revision) {
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
	carried := map[string]*Carry{}
	for i, k := range carries {
		if k.Contract != "" && k.Contract == contracts[k.Check] {
			carried[k.Check] = &carries[i]
		}
	}
	allStates := map[string]model.CheckState{}
	checks := map[string]model.Check{}
	for _, x := range c.Checks {
		checks[x.ID] = x
		st := EvaluateCarried(x, carried[x.ID], histories[x.ID], now)
		st.Contract = public[x.ID]
		if st.Last != nil {
			last := *st.Last
			last.Contract, last.Revision = st.Contract, ""
			st.Last = &last
		}
		allStates[x.ID] = st
		if view == "" || view == x.Observer {
			out.Checks = append(out.Checks, allStates[x.ID])
		}
	}
	for _, o := range c.Observers {
		s := model.ObserverState{ID: o.ID, Health: "unknown", LastSeen: lastSeen[o.ID], Reason: "no recent executor evidence", Contracts: reported[o.ID]}
		if !s.LastSeen.IsZero() && now.Sub(s.LastSeen) <= time.Duration(o.StaleSeconds)*time.Second {
			s.Health = "up"
			s.Reason = "recent executor evidence; target health evaluated separately"
		}
		if s.Contracts == "outdated" {
			s.Reason += "; it measures an outdated topology, so samples of checks that changed are refused until it reloads"
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
		// A node's evidence comes from checks on its own endpoints and on
		// endpoints reached through it as an edge or proxy.
		states := group(func(x model.Check) bool {
			ep, ok := endpoints[x.Endpoint]
			return ok && (ep.Node == n.ID || slices.Contains(ep.Via, n.ID))
		})
		s := model.EntityState{ID: n.ID, Health: "unknown", Evidence: "unknown", Reason: "no fresh remote-response evidence"}
		failures := 0
		proof := []string{}
		through := false
		for _, cs := range states {
			s.Checks = append(s.Checks, cs.ID)
			if cs.Freshness != "fresh" || cs.Last == nil {
				continue
			}
			x := checks[cs.ID]
			ep := endpoints[x.Endpoint]
			if answered(ep, n.ID, cs.Last) {
				proof = append(proof, cs.ID)
			}
			through = through || behind(ep, n.ID, cs.Last)
			if cs.Health == "down" && implicated(ep, n.ID, cs.Last) {
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
		} else if through {
			s.Reason = "answered only through the edge or proxy in front, which proves that hop alone; a check that does not go through it proves this node"
		}
		if len(states) == 0 {
			s.Reason = "registered asset; no checks in this view"
		}
		out.Nodes = append(out.Nodes, s)
	}
	// An endpoint is judged from the checks that target it.
	for _, e := range c.Endpoints {
		out.Endpoints = append(out.Endpoints, Aggregate(e.ID, group(func(x model.Check) bool { return x.Endpoint == e.ID }), "no check targets this endpoint in this view"))
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
		s := Aggregate(id, pathEvidenceStates(group(func(x model.Check) bool { return x.Path == id })), "no endpoint-to-endpoint check; segment health unknown")
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
		s := Aggregate(l.ID, pathEvidenceStates(group(func(x model.Check) bool { return x.Link == l.ID })), "segment not independently measured")
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
		s := Aggregate(id, group(func(x model.Check) bool { return x.Service == id }), "registered, not monitored")
		for _, dep := range def.DependsOn {
			v := evalService(dep)
			if v.Health == "down" || v.Affected {
				s.Affected = true
				s.Reason += "; dependency affected: " + dep
			}
		}
		for _, x := range c.Checks {
			if x.Service == id && x.Path != "" && relevant(x) && evalPath(x.Path).Health == "down" {
				if !strings.HasSuffix(s.Reason, unavailable) {
					s.Reason += unavailable
				}
				s.Affected = true
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
	scopeOf := func(x model.Check) string {
		switch {
		case x.Path != "":
			return x.Observer + "/path/" + x.Path
		case x.Endpoint != "":
			return x.Observer + "/asset/" + endpoints[x.Endpoint].Node
		}
		return x.Observer + "/" + x.ID
	}
	// An incident's ID names its scope after what the checks in that scope
	// measure, so it holds across edits that change nothing measured.
	measured := map[string][]string{}
	for _, x := range c.Checks {
		measured[scopeOf(x)] = append(measured[scopeOf(x)], public[x.ID])
	}
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
		base := scopeOf(x)
		scope := base
		if policy {
			scope += "/policy"
		}
		inc := incidents[scope]
		if inc == nil {
			inc = &model.Incident{ID: digest(measured[base])[:12] + "/" + scope, Scope: scope, Summary: "confirmed failure of the listed checks from this observer", Checks: []string{}, CounterEvidence: []string{}, Since: s.Since}
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
			ep := endpoints[x.Endpoint]
			for _, other := range c.Checks {
				cs := allStates[other.ID]
				oe, ok := endpoints[other.Endpoint]
				if other.Observer != x.Observer && ok && (oe.Node == ep.Node || slices.Contains(oe.Via, ep.Node)) && cs.Freshness == "fresh" && cs.Last != nil && answered(oe, ep.Node, cs.Last) {
					inc.CounterEvidence = boundedChecks(inc.CounterEvidence, []string{other.ID})
				}
			}
			switch {
			case len(ep.Via) > 0 && s.Last.Connectivity:
				// The edge in front answered; whatever failed is behind it.
				inc.Hypothesis = "the edge in front answered" + withStatus(s.Last) + "; the fault lies behind it, at the origin or on the edge's leg to it, and the origin's own state is not established"
			case len(inc.CounterEvidence) > 0:
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
	// Incidents read in the order of their scopes: by observer, then what failed.
	sort.Slice(out.Incidents, func(i, j int) bool { return out.Incidents[i].Scope < out.Incidents[j].Scope })
	return out
}

const unavailable = "; access path unavailable, intrinsic service state not established"

// answered reports whether v is remote-response evidence for node: the
// endpoint's own node, or the first edge or proxy in its via chain. The
// first hop answers whatever happens behind it, even with an error page,
// so any response proves it and nothing behind it. Where an edge says a
// response came from, its cache status among them, is the response's own
// word, which the edge or anything behind it can send whatever happened,
// so the nodes behind the first hop count only a check that reaches them
// without going through it.
func answered(ep model.Endpoint, node string, v *model.Observation) bool {
	return responded(v) && (len(ep.Via) == 0 || ep.Via[0] == node)
}

// behind reports whether v is an answer through the first hop of ep's via
// chain that proves that hop and not node, behind it.
func behind(ep model.Endpoint, node string, v *model.Observation) bool {
	return responded(v) && len(ep.Via) > 0 && ep.Via[0] != node
}

// responded reports whether v is a measured response from the remote end.
func responded(v *model.Observation) bool {
	return v.Evidence == "measured" && v.Connectivity && v.Facts["response_proof"] != "" && v.Facts["response_proof"] != "none"
}

// implicated reports whether a confirmed failure of a check on ep may lie at
// node: anywhere along the chain, except at a first hop that answered.
func implicated(ep model.Endpoint, node string, v *model.Observation) bool {
	return len(ep.Via) == 0 || ep.Via[0] != node || !v.Connectivity
}

func withStatus(v *model.Observation) string {
	if code := v.Facts["http_status"]; code != "" {
		return " with HTTP " + code
	}
	return ""
}

// digest is the SHA-256 of a set of strings, in hex.
func digest(set []string) string {
	sorted := slices.Sorted(slices.Values(set))
	h := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(h[:])
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

// Aggregate is the state of an entity from the checks that measure it:
// up when all pass, down when all confirm failure, degraded when some
// pass or any is degraded, otherwise unknown. Checks without current
// evidence count as unknown; empty is the reason when none measures it.
func Aggregate(id string, states []model.CheckState, empty string) model.EntityState {
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
