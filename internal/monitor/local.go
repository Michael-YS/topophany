package monitor

import (
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// Local retains one observation and one bounded threshold carry per local
// check. It is display evidence, independent of durable outbox retention.
type Local struct {
	mu       sync.Mutex
	topology model.Config
	observer string
	latest   map[string]model.Observation
	carries  map[string]engine.Carry
}

func NewLocal(c model.Config, observer string) *Local {
	l := &Local{observer: observer, latest: map[string]model.Observation{}, carries: map[string]engine.Carry{}}
	l.Reload(c)
	return l
}
func (l *Local) Reload(c model.Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.topology = config.Clone(c)
	contracts := config.Contracts(c)
	for id, v := range l.latest {
		if contracts[id] != v.Contract {
			delete(l.latest, id)
			delete(l.carries, id)
		}
	}
}
func (l *Local) Record(v model.Observation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.topology.Checks {
		if c.ID != v.Check || c.Observer != l.observer || v.Contract != config.Contract(l.topology, c) {
			continue
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
		return
	}
}
func (l *Local) Snapshot(now time.Time) model.Snapshot {
	l.mu.Lock()
	c := l.topology
	rows := make([]model.Observation, 0, len(l.latest))
	carries := make([]engine.Carry, 0, len(l.carries))
	for _, v := range l.latest {
		rows = append(rows, v)
	}
	for _, v := range l.carries {
		carries = append(carries, v)
	}
	l.mu.Unlock()
	beats := map[string]model.Heartbeat{l.observer: {Observer: l.observer, SampledAt: now, Contracts: config.ObserverContracts(c, l.observer)}}
	return engine.BuildCarried(c, "worker", l.observer, rows, carries, beats, now)
}

// Feed holds an immutable source snapshot. Set transfers ownership; callers
// must not mutate its maps/slices afterwards. Fetch readers only project it.
type Feed struct {
	mu    sync.Mutex
	input Input
}

func (f *Feed) Set(in Input) { f.mu.Lock(); f.input = in; f.mu.Unlock() }
func (f *Feed) Get() Input   { f.mu.Lock(); defer f.mu.Unlock(); return f.input }
