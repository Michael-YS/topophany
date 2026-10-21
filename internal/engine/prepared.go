package engine

import (
	"slices"
	"sort"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// Prepared owns the time-independent judgments of one evidence version. The
// bounded state tails are folded once, then reused across views and clock ticks.
// Nothing published by Build aliases this state.
type Prepared struct {
	plan     *Plan
	checks   map[string]model.CheckState
	seen     map[string][]time.Time
	reported map[string]string
	beats    map[string]model.Heartbeat
}

func (p *Plan) Prepare(rows []model.Observation, carries []Carry, beats map[string]model.Heartbeat) *Prepared {
	e := &Prepared{plan: p, seen: map[string][]time.Time{}, reported: map[string]string{}, beats: map[string]model.Heartbeat{}}
	for id, h := range beats {
		if h.Project != p.config.Project {
			continue
		}
		e.beats[id] = model.CloneHeartbeat(h)
		e.seen[id] = append(e.seen[id], h.SampledAt)
		e.reported[id] = p.reportedContract(id, h)
	}
	e.checks = p.foldEvidence(rows, carries, func(observer string, at time.Time) {
		e.seen[observer] = append(e.seen[observer], at)
	})
	for id, stamps := range e.seen {
		slices.SortFunc(stamps, time.Time.Compare)
		e.seen[id] = slices.CompactFunc(stamps, time.Time.Equal)
	}
	return e
}

func (p *Plan) reportedContract(observer string, h model.Heartbeat) string {
	var current bool
	switch {
	case h.Contracts != "":
		current = h.Contracts == p.observers[observer]
	case h.Revision != "":
		current = h.Revision == p.revision
	default:
		return ""
	}
	if current {
		return "current"
	}
	return "outdated"
}

// Both one-shot and reusable builds fold evidence through the same path. The
// visitor lets one-shot builds retain just last-seen times, while Prepared
// keeps all eligible timestamps to answer future reads and clock rollbacks.
// Returned states own their diagnostics independently of the input rows.
func (p *Plan) foldEvidence(rows []model.Observation, carries []Carry, seen func(string, time.Time)) map[string]model.CheckState {
	histories := make(map[string][]*model.Observation, len(p.checks))
	for i := range rows {
		v := &rows[i]
		if v.Project != p.config.Project || !config.Counted(*v, p.contracts, p.revision) {
			continue
		}
		x, ok := p.checks[v.Check]
		if !ok || x.Observer != v.Observer {
			continue
		}
		histories[v.Check] = append(histories[v.Check], v)
		seen(v.Observer, v.SampledAt)
	}
	carried := map[string]*Carry{}
	for i, k := range carries {
		if k.Contract != "" && k.Contract == p.contracts[k.Check] {
			carried[k.Check] = &carries[i]
		}
	}
	states := make(map[string]model.CheckState, len(p.checks))
	for _, x := range p.config.Checks {
		st := evaluateEvidence(x, carried[x.ID], histories[x.ID])
		st.Contract = p.publicContracts[x.ID]
		if st.Last != nil {
			last := model.CloneObservation(*st.Last)
			last.Contract, last.Revision = st.Contract, ""
			st.Last = &last
		}
		states[x.ID] = st
	}
	return states
}

// Future evidence becomes eligible as the clock advances, and a clock rollback
// can make it ineligible again. Keep timestamps rather than a frozen last-seen.
func (e *Prepared) lastSeen(now time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(e.seen))
	limit := now.Add(5 * time.Second)
	for id, stamps := range e.seen {
		i := sort.Search(len(stamps), func(i int) bool { return stamps[i].After(limit) })
		if i > 0 {
			out[id] = stamps[i-1]
		}
	}
	return out
}
