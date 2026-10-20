package view

import (
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A policy incident has no failed request and so no other observer to
// contrast it with and no hypothesis the engine could draw: what it has is
// what its samples observed. The view says that, in the sample's own words
// and with the values the declaration was compared with.

// policyIndex is what an incident's checks are looked up in: their
// declarations and their newest samples, indexed once per derivation.
type policyIndex struct {
	defs   map[string]model.Check
	states map[string]model.CheckState
}

func (rb *rowBuilder) policyIndex() *policyIndex {
	if rb.policy == nil {
		s := rb.view.Snapshot
		p := &policyIndex{defs: make(map[string]model.Check, len(s.Topology.Checks)), states: make(map[string]model.CheckState, len(s.Checks))}
		for _, c := range s.Topology.Checks {
			p.defs[c.ID] = c
		}
		for _, c := range s.Checks {
			p.states[c.ID] = c
		}
		rb.policy = p
	}
	return rb.policy
}

// incidentHypothesis is the line the incident's details show under its
// hypothesis or policy heading: the engine's hypothesis, else, when every
// check in the incident is a policy sample, what those samples say was
// violated, a line each (see policyFinding). Empty when there is nothing to
// say, so no heading stands over nothing.
func (rb *rowBuilder) incidentHypothesis(i model.Incident) string {
	if i.Hypothesis != "" || len(i.Checks) == 0 {
		return i.Hypothesis
	}
	p := rb.policyIndex()
	lines := []string{}
	for _, id := range i.Checks {
		cs, ok := p.states[id]
		if !ok || cs.Last == nil || cs.Last.Outcome != "policy" {
			return ""
		}
		line := cs.Last.Detail
		if found := policyFinding(p.defs[id], cs.Last); found != "" {
			line += " (" + found + ")"
		}
		if line != "" && !slices.Contains(lines, line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// policyFinding is what a policy sample reported against what its check
// declares, for the policies that compare a reported word or colo with a
// declared one: "answered from LAX; declared HKG, NRT", "WARP off; declared
// on". A value the sample did not keep (a colo withheld from a secret
// request) is left out and the declaration is still said. Empty for any
// other policy, whose detail already says it all.
func policyFinding(x model.Check, o *model.Observation) string {
	say := func(reported, declared string) string {
		parts := []string{}
		if reported != "" {
			parts = append(parts, reported)
		}
		if declared != "" {
			parts = append(parts, "declared "+declared)
		}
		return strings.Join(parts, "; ")
	}
	trace := model.TraceExpectations{}
	if x.HTTP.ExpectedTrace != nil {
		trace = *x.HTTP.ExpectedTrace
	}
	word := func(label, got string) string {
		if got == "" {
			return ""
		}
		return label + " " + got
	}
	colo := func(got string) string {
		if got == "" {
			return ""
		}
		return "answered from " + got
	}
	switch o.Code {
	case "pop_mismatch":
		return say(colo(o.Facts["edge_pop"]), strings.Join(x.HTTP.ExpectedPops, ", "))
	case "colo_mismatch":
		return say(colo(o.Facts["trace_colo"]), strings.Join(trace.Colos, ", "))
	case "warp_mismatch":
		return say(word("WARP", o.Facts["trace_warp"]), trace.Warp)
	case "gateway_mismatch":
		return say(word("Gateway", o.Facts["trace_gateway"]), trace.Gateway)
	}
	return ""
}
