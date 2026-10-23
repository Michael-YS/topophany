package monitor

import (
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

// Local retains one observation and one bounded threshold carry per local
// check. It is display evidence, independent of durable outbox retention.
type Local struct {
	mu               sync.Mutex
	project          string
	sources          map[string]model.Source
	external         *model.ExternalState
	externalPrepared *engine.Prepared
	externalSizes    report.StateSizes
	plan             *engine.Plan
	contracts        map[string]string
	checks           map[string]model.Check
	digest           string
	observer         string
	latest           map[string]model.Observation
	carries          map[string]engine.Carry
}

func NewLocal(c model.Config, observer string) *Local {
	l := &Local{observer: observer, latest: map[string]model.Observation{}, carries: map[string]engine.Carry{}}
	l.Reload(c)
	return l
}
func (l *Local) Reload(c model.Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.plan = engine.NewPlan(c)
	contracts := config.Contracts(c)
	l.contracts = contracts
	previousChecks := l.checks
	l.checks = map[string]model.Check{}
	for _, check := range c.Checks {
		if check.Observer == l.observer {
			l.checks[check.ID] = check
		}
	}
	l.project = c.Project
	l.sources = map[string]model.Source{}
	for _, src := range c.Sources {
		if src.Observer == l.observer {
			l.sources[src.ID] = src
		}
	}
	next := model.NewExternalState()
	if l.external != nil {
		for id, state := range l.external.Bindings {
			if x, ok := l.checks[id]; ok && state.Contract == contracts[id] {
				next.Bindings[id] = report.ApplyRule(state, model.ExternalRule{FailureThreshold: x.FailureThreshold, RecoveryThreshold: x.RecoveryThreshold, PolicyThreshold: x.PolicyThreshold})
			}
		}
		for id, state := range l.external.Sources {
			if _, ok := l.sources[id]; ok {
				next.Sources[id] = state
			}
		}
	}
	l.external = next
	l.externalSizes, _ = report.MeasureState(next)
	l.externalPrepared = l.plan.PrepareExternal(nil, nil, nil, next)
	l.digest = config.ObserverContracts(c, l.observer)
	for id, v := range l.latest {
		previous := engine.EvidencePolicy{Contract: v.Contract, Stale: time.Duration(previousChecks[id].StaleSeconds) * time.Second}
		next := engine.EvidencePolicy{Contract: contracts[id], Stale: time.Duration(l.checks[id].StaleSeconds) * time.Second}
		if previous.ChangeTo(next) == engine.ResetMeasurement {
			delete(l.latest, id)
			delete(l.carries, id)
		} else if !l.carries[id].Compatible(next) {
			delete(l.carries, id)
		}
	}
}
func (l *Local) Record(v model.Observation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.checks[v.Check]
	if !ok || v.Observer != l.observer || v.Contract != l.contracts[v.Check] {
		return
	}
	if c.Kind == "external" {
		l.recordExternal(v, c)
		return
	}
	k := l.carries[c.ID]
	if previous, ok := l.latest[c.ID]; ok {
		k.Fold(previous, time.Duration(c.StaleSeconds)*time.Second)
	}
	if k.Holds(v) {
		return
	}
	l.carries[c.ID] = k
	l.latest[c.ID] = model.SummaryObservation(v)
}
func (l *Local) Snapshot(now time.Time) model.Snapshot {
	l.mu.Lock()
	prepared, digest, project := l.externalPrepared, l.digest, l.project
	rows := make([]model.Observation, 0, len(l.latest))
	carries := make([]engine.Carry, 0, len(l.carries))
	for _, v := range l.latest {
		rows = append(rows, v)
	}
	for _, v := range l.carries {
		carries = append(carries, v)
	}
	l.mu.Unlock()
	beats := map[string]model.Heartbeat{l.observer: {Project: project, Observer: l.observer, SampledAt: now, Contracts: digest}}
	return prepared.WithNativeEvidence(rows, carries, beats).Build("worker", l.observer, now)
}

// Feed holds an immutable source snapshot. Set transfers ownership; callers
// must not mutate its maps/slices afterwards. Fetch readers only project it.
type Feed struct {
	mu    sync.Mutex
	input Input
}

func (f *Feed) Set(in Input) { f.mu.Lock(); f.input = in; f.mu.Unlock() }
func (f *Feed) Get() Input   { f.mu.Lock(); defer f.mu.Unlock(); return f.input }
