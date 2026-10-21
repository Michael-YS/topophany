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
	e := &Prepared{plan: p, checks: make(map[string]model.CheckState, len(p.checks)), seen: map[string][]time.Time{}, reported: map[string]string{}, beats: map[string]model.Heartbeat{}}
	for id, h := range beats {
		if h.Project != p.config.Project {
			continue
		}
		e.beats[id] = model.CloneHeartbeat(h)
		e.seen[id] = append(e.seen[id], h.SampledAt)
		switch {
		case h.Contracts != "":
			e.reported[id] = map[bool]string{true: "current", false: "outdated"}[h.Contracts == p.observers[id]]
		case h.Revision != "":
			e.reported[id] = map[bool]string{true: "current", false: "outdated"}[h.Revision == p.revision]
		}
	}
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
		e.seen[v.Observer] = append(e.seen[v.Observer], v.SampledAt)
	}
	carried := map[string]*Carry{}
	for i, k := range carries {
		if k.Contract != "" && k.Contract == p.contracts[k.Check] {
			carried[k.Check] = &carries[i]
		}
	}
	for _, x := range p.config.Checks {
		st := evaluateEvidence(x, carried[x.ID], histories[x.ID])
		st.Contract = p.publicContracts[x.ID]
		if st.Last != nil {
			last := model.CloneObservation(*st.Last)
			last.Contract, last.Revision = st.Contract, ""
			st.Last = &last
		}
		e.checks[x.ID] = st
	}
	for id, stamps := range e.seen {
		slices.SortFunc(stamps, time.Time.Compare)
		e.seen[id] = slices.CompactFunc(stamps, time.Time.Equal)
	}
	return e
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
