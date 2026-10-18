package monitor

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// A projection owns the indexes shared by every structured row in one frame.
// Keep collector judgments and their original clocks together: locally aging
// only checks would leave entity and incident judgments backed by expired data.
type projection struct {
	v, all                        view.View
	fleet                         *view.Fleet
	nodeChecks                    map[string][]string
	serviceChecks                 map[string][]string
	observers                     []ObserverVerdict
	currentClocks, completeClocks *evidenceIndex
}

type evidenceClock struct{ sampled, until time.Time }

func newProjection(in Input, now time.Time) *projection {
	v := view.View{Snapshot: in.Snapshot, Now: now, Cached: view.Expired(in.Snapshot, now)}
	all := v
	if in.AllSnapshot != nil {
		all.Snapshot = *in.AllSnapshot
		all.Cached = view.Expired(all.Snapshot, now)
	} else if in.Snapshot.ViewObserver != "" {
		all.Cached = true
	}
	return &projection{v: v, all: all}
}

func (p *projection) serverFleet() *view.Fleet {
	if p.fleet == nil {
		p.fleet = p.v.Fleet()
		p.nodeChecks = make(map[string][]string, len(p.v.Snapshot.Nodes))
		for _, n := range p.v.Snapshot.Nodes {
			p.nodeChecks[n.ID] = n.Checks
		}
		p.serviceChecks = make(map[string][]string, len(p.v.Snapshot.Services))
		for _, s := range p.v.Snapshot.Services {
			p.serviceChecks[s.ID] = s.Checks
		}
	}
	return p.fleet
}

func (p *projection) observerValues() []ObserverVerdict {
	if p.observers == nil {
		p.observers = observerVerdicts(p.all)
	}
	return p.observers
}

func (p *projection) resolve(value Value) Value {
	s := p.v.Snapshot
	if s.GeneratedAt.IsZero() || s.GeneratedAt.After(p.v.Now.Add(5*time.Second)) {
		return missing(value, "source_expired")
	}
	if (value.Metric == "observers.verdicts" || value.Metric == "server.card") && p.all.Snapshot.GeneratedAt.After(p.v.Now.Add(5*time.Second)) {
		return missing(value, "source_expired")
	}
	value = p.richValue(value)
	if value.Value != nil && !p.v.Now.Before(value.ValidUntil) {
		value.Freshness = "stale"
		return missing(value, "sample_expired")
	}
	return value
}

func (p *projection) evidence() *evidenceIndex {
	if p.currentClocks == nil {
		p.currentClocks = indexEvidence(p.v)
	}
	return p.currentClocks
}
func (p *projection) completeEvidence() *evidenceIndex {
	if p.completeClocks == nil {
		p.completeClocks = indexEvidence(p.all)
	}
	return p.completeClocks
}

// Index the source's original clocks once, before any local aging. An expired
// fresh observation still bounds every conclusion that depended on it.
type evidenceIndex struct {
	base                      time.Time
	checks                    map[string]evidenceClock
	byObserver                map[string]evidenceClock
	checkClock, observerClock evidenceClock
}

func combineClock(a, b evidenceClock) evidenceClock {
	if b.sampled.After(a.sampled) {
		a.sampled = b.sampled
	}
	a.until = minTime(a.until, b.until)
	return a
}
func indexEvidence(v view.View) *evidenceIndex {
	s := v.Snapshot
	base := s.GeneratedAt.Add(15 * time.Second).UTC()
	e := &evidenceIndex{base: base, checks: make(map[string]evidenceClock, len(s.Checks)), byObserver: make(map[string]evidenceClock, len(s.Observers)), checkClock: evidenceClock{until: base}, observerClock: evidenceClock{until: base}}
	stale := make(map[string]int, len(s.Topology.Checks))
	for _, c := range s.Topology.Checks {
		stale[c.ID] = c.StaleSeconds
	}
	for _, c := range s.Checks {
		clock := evidenceClock{until: base}
		if c.Last != nil {
			clock.sampled = c.Last.SampledAt.UTC()
			if c.Freshness == "fresh" {
				clock.until = minTime(base, clock.sampled.Add(time.Duration(stale[c.ID])*time.Second))
				if clock.sampled.After(v.Now.Add(5 * time.Second)) {
					clock.until = v.Now
				}
			}
		}
		e.checks[c.ID] = clock
		e.checkClock = combineClock(e.checkClock, clock)
		own, ok := e.byObserver[c.Observer]
		if !ok {
			own = evidenceClock{until: base}
		}
		e.byObserver[c.Observer] = combineClock(own, clock)
	}
	stale = make(map[string]int, len(s.Topology.Observers))
	for _, o := range s.Topology.Observers {
		stale[o.ID] = o.StaleSeconds
	}
	for _, o := range s.Observers {
		clock := evidenceClock{sampled: o.LastSeen.UTC(), until: base}
		if o.Health == "up" && !o.LastSeen.IsZero() {
			clock.until = minTime(base, o.LastSeen.Add(time.Duration(stale[o.ID])*time.Second))
			if o.LastSeen.After(v.Now.Add(5 * time.Second)) {
				clock.until = v.Now
			}
		}
		e.observerClock = combineClock(e.observerClock, clock)
		own, ok := e.byObserver[o.ID]
		if !ok {
			own = evidenceClock{until: base}
		}
		e.byObserver[o.ID] = combineClock(own, clock)
	}
	return e
}
func (e *evidenceIndex) times(ids map[string]bool, observers bool) (time.Time, time.Time) {
	clock := e.checkClock
	if ids != nil {
		clock = evidenceClock{until: e.base}
		for id := range ids {
			if c, ok := e.checks[id]; ok {
				clock = combineClock(clock, c)
			}
		}
	}
	if observers {
		clock = combineClock(clock, e.observerClock)
	}
	return clock.sampled, clock.until
}
