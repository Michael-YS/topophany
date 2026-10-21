package engine

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

// externalBaseState is the state of a binding with no accepted report yet.
func (p *Plan) externalBaseState(x model.Check) model.CheckState {
	return model.CheckState{ID: x.ID, Contract: p.publicContracts[x.ID], Observer: x.Observer, Health: "unknown", Freshness: "never", Evidence: "reported", Reason: "no accepted external report"}
}

// currentExternal judges one binding at now from persisted acceptance state.
// Collection attempts and report evidence stay separate: a source error does
// not become a target failure, and an expired report is unknown rather than a
// fabricated recovery or outage.
func (p *Plan) currentExternal(x model.Check, ext *model.ExternalState, now time.Time) (model.CheckState, model.ExternalBindingView) {
	st := p.externalBaseState(x)
	view := model.ExternalBindingView{Check: x.ID, Source: x.External.Source, Object: x.External.Object, ReportType: x.External.Report, Node: x.External.Node, Service: x.Service, Health: "unknown", Freshness: "never", Evidence: "reported", Reason: st.Reason}
	if ext == nil {
		return st, view
	}
	state, ok := ext.Bindings[x.ID]
	if !ok {
		if source, found := p.sources[x.External.Source]; found {
			if ss, ok := ext.Sources[source.ID]; ok {
				view.SourceState = externalSourceView(ss)
			}
		}
		return st, view
	}
	source := p.sources[state.Source]
	if ss, ok := ext.Sources[state.Source]; ok {
		view.SourceState = externalSourceView(ss)
	}
	view.Report = state.Last
	view.Stats = externalStats(state)
	view.TimeBasis = state.Cursor.TimeBasis
	if state.Last != nil {
		view.SampledAt, _ = report.SampleAt(*state.Last)
	}
	dataDeadline := state.DataDeadline
	if dataDeadline.IsZero() && state.Last != nil {
		if at, known := report.SampleAt(*state.Last); known {
			dataDeadline = at.Add(time.Duration(x.External.MaxAgeSeconds) * time.Second)
		}
	}
	view.DataDeadline = dataDeadline
	if contact, known := report.ContactDeadline(stateContact(ext, state.Source), time.Duration(source.StaleSeconds)*time.Second); known {
		view.ContactDeadline = contact
	}
	view.ValidUntil = report.ValidUntil(dataDeadline, view.ContactDeadline, now.Add(model.SnapshotLifetime))
	st.Since = state.LastAcceptedAt
	if x.Disabled {
		st.Freshness, st.Reason = "paused", "check paused"
		view.Freshness, view.Reason = st.Freshness, st.Reason
		return st, view
	}
	if x.Maintenance {
		st.Freshness, st.Reason = "maintenance", "maintenance; excluded from alerts"
		view.Freshness, view.Reason = st.Freshness, st.Reason
		return st, view
	}
	if state.Last == nil {
		st.Reason = "no accepted external report"
		if view.SourceState.LastResult == "error" {
			st.Reason += "; source read failed"
		}
		view.Reason = st.Reason
		return st, view
	}
	if at, known := report.SampleAt(*state.Last); known && at.After(now.Add(model.SampleClockAhead)) {
		st.Health, st.Freshness, st.Reason = "unknown", "stale", "external sample clock is ahead; report not treated as current"
		st.PolicyConfirmed = false
		view.Health, view.Freshness, view.Reason = st.Health, st.Freshness, st.Reason
		view.ValidUntil = now
		return st, view
	}
	switch {
	case dataDeadline.IsZero() || !now.Before(dataDeadline):
		st.Health, st.Freshness, st.Reason = "unknown", "stale", "external report exceeded max_age; last report is historical"
		st.PolicyConfirmed = false
		view.Health, view.Freshness, view.Reason = st.Health, st.Freshness, st.Reason
		return st, view
	case view.ContactDeadline.IsZero():
		st.Health, st.Freshness, st.Reason = "unknown", "stale", "no recent successful source contact; last report is historical"
		st.PolicyConfirmed = false
		view.Health, view.Freshness, view.Reason = st.Health, st.Freshness, st.Reason
		return st, view
	case !now.Before(view.ContactDeadline):
		st.Health, st.Freshness, st.Reason = "unknown", "stale", "source contact expired; last report is historical"
		st.PolicyConfirmed = false
		view.Health, view.Freshness, view.Reason = st.Health, st.Freshness, st.Reason
		return st, view
	}
	st.Freshness = "fresh"
	if state.Last.Status != nil {
		view.Health, st.Health, st.Reason = statusHealth(x, state)
		view.Reason = st.Reason
		view.Freshness = st.Freshness
		return st, view
	}
	if state.Last.Resource != nil {
		policy := report.Evaluate(*x.External, *state.Last)
		view.Policy = &model.ExternalPolicyView{Outcome: policy.Outcome, Violations: append([]string(nil), policy.Violations...), Missing: append([]string(nil), policy.Missing...), Confirmed: state.PolicyConfirmed}
		st.PolicyConfirmed = state.PolicyConfirmed
		switch policy.Outcome {
		case report.PolicyViolation:
			st.Health = "degraded"
			st.Reason = "reported resource policy violation: " + join(policy.Violations)
			if !state.PolicyConfirmed {
				st.Reason += "; confirmation threshold not reached"
			}
		case report.PolicyUnknown:
			st.Health = "unknown"
			st.Reason = "reported resource assertion cannot be evaluated: missing " + join(policy.Missing)
		default:
			st.Health = "up"
			st.Reason = "reported resource reading is current; it does not prove application reachability"
		}
		view.Health, view.Reason, view.Freshness = st.Health, st.Reason, st.Freshness
		return st, view
	}
	// Normalization admits exactly one payload; a state that reaches here is
	// corrupt, so expose unknown rather than guessing.
	st.Health, st.Reason = "unknown", "external report payload is unavailable"
	view.Health, view.Reason = st.Health, st.Reason
	return st, view
}

func statusHealth(x model.Check, state model.BindingState) (health, checkHealth, reason string) {
	status := state.Last.Status.Status
	switch status {
	case "up":
		checkHealth = "up"
		reason = "upstream reports up"
		if state.ConfirmedDown && int(state.UpRun) < max(x.RecoveryThreshold, 1) {
			checkHealth = "degraded"
			reason = "upstream reports up; recovery threshold not reached"
		}
	case "down":
		checkHealth = "degraded"
		reason = "upstream reports down; confirmation threshold not reached"
		if state.ConfirmedDown {
			checkHealth = "down"
			reason = "upstream reports down"
		}
	case "degraded":
		checkHealth = "degraded"
		reason = "upstream reports degraded"
	default:
		checkHealth = "unknown"
		reason = "upstream status is unknown"
	}
	return checkHealth, checkHealth, reason
}

func stateContact(ext *model.ExternalState, source string) time.Time {
	if ext == nil {
		return time.Time{}
	}
	return ext.Sources[source].LastContact
}

func externalSourceView(s model.SourceState) model.ExternalSourceView {
	return model.ExternalSourceView{LastContact: s.LastContact, LastAttemptAt: s.LastAttemptAt, LastResult: s.LastResult, LastCode: s.LastCode, LastDurationMS: s.LastDurationMS}
}

func externalStats(b model.BindingState) model.ExternalStats {
	s := model.ExternalStats{Samples: b.ReportSamples, ResourceSamples: b.ResourceSamples, StatusSamples: b.StatusSamples, StatusUp: b.StatusUp, StatusDegraded: b.StatusDegraded, StatusDown: b.StatusDown, StatusUnknown: b.StatusUnknown, Duplicates: b.Duplicates, Late: b.Late, Conflicts: b.Conflicts, Unusable: b.Unusable, FirstAcceptedAt: b.FirstAcceptedAt, LastAcceptedAt: b.LastAcceptedAt, UpstreamLatencySamples: b.UpstreamLatencySamples}
	if !b.FirstAcceptedAt.IsZero() && !b.LastAcceptedAt.IsZero() && b.LastAcceptedAt.After(b.FirstAcceptedAt) {
		s.ObservedForSeconds = int64(b.LastAcceptedAt.Sub(b.FirstAcceptedAt) / time.Second)
	}
	if b.UpstreamLatencySamples > 0 {
		avg := float64(b.UpstreamLatencySumMS) / float64(b.UpstreamLatencySamples)
		s.UpstreamLatencyAvgMS = &avg
		max := b.UpstreamLatencyMaxMS
		s.UpstreamLatencyMaxMS = &max
	}
	return s
}

func join(parts []string) string {
	if len(parts) == 0 {
		return "unknown"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}
