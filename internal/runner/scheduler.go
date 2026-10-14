// Package runner provides a bounded, non-overlapping, jittered scheduler. A
// storage failure stops scheduling rather than silently dropping measurements.
package runner

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Probe func(context.Context, model.Config, model.Check) model.Observation
type Sink func(model.Observation) error
type Scheduler struct {
	Config    model.Config
	Observer  string
	Workers   int
	Boot      string
	Probe     Probe
	Sink      Sink
	sequence  uint64
	contracts map[string]string
}
type completed struct {
	index       int
	observation model.Observation
}

func New(c model.Config, observer string, workers int, p Probe, sink Sink) (*Scheduler, error) {
	if e := config.Validate(c); e != nil {
		return nil, e
	}
	found := false
	for _, o := range c.Observers {
		found = found || o.ID == observer
	}
	if !found {
		return nil, fmt.Errorf("unknown local observer %q", observer)
	}
	if workers < 1 || workers > 32 || p == nil || sink == nil {
		return nil, fmt.Errorf("invalid scheduler settings")
	}
	boot, e := secure.Token()
	if e != nil {
		return nil, e
	}
	return &Scheduler{Config: c, Observer: observer, Workers: workers, Boot: boot, Probe: p, Sink: sink}, nil
}
func (s *Scheduler) stamp(v model.Observation) model.Observation {
	s.sequence++
	if s.contracts == nil {
		s.contracts = config.Contracts(s.Config)
	}
	v.Project = s.Config.Project
	v.Contract = s.contracts[v.Check]
	v.Revision = ""
	v.Observer = s.Observer
	v.Boot = s.Boot
	v.Sequence = s.sequence
	v.ID = fmt.Sprintf("%s.%s.%d", s.Observer, s.Boot, s.sequence)
	v.ReceivedAt = time.Time{}
	return v
}
func (s *Scheduler) checks() []model.Check {
	r := []model.Check{}
	for _, x := range s.Config.Checks {
		if x.Observer == s.Observer && !x.Disabled && !x.Maintenance {
			r = append(r, x)
		}
	}
	return r
}

// Run is single-owner; do not call Run and Once concurrently on one scheduler.
func (s *Scheduler) Run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	checks := s.checks()
	jobs := make(chan int)
	done := make(chan completed, s.Workers)
	var wg sync.WaitGroup
	for i := 0; i < s.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case n, ok := <-jobs:
					if !ok {
						return
					}
					v := s.Probe(ctx, s.Config, checks[n])
					select {
					case done <- completed{n, v}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	defer func() { cancel(); wg.Wait() }()
	due := make([]time.Time, len(checks))
	active := make([]bool, len(checks))
	busy := map[string]bool{}
	now := time.Now()
	for i := range due {
		due[i] = now.Add(time.Duration(rand.IntN(1000)) * time.Millisecond)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	key := func(x model.Check) string { return x.Observer + "/" + config.Target(s.Config, x) }
	for {
		select {
		case <-ctx.Done():
			return nil
		case r := <-done:
			active[r.index] = false
			delete(busy, key(checks[r.index]))
			if ctx.Err() != nil {
				return nil
			}
			if e := s.Sink(s.stamp(r.observation)); e != nil {
				return e
			}
			interval := time.Duration(checks[r.index].IntervalSeconds) * time.Second
			due[r.index] = time.Now().Add(interval + time.Duration(float64(interval)*(rand.Float64()-.5)*.1))
		case now := <-tick.C:
			for i, x := range checks {
				if active[i] || busy[key(x)] || now.Before(due[i]) {
					continue
				}
				select {
				case jobs <- i:
					active[i] = true
					busy[key(x)] = true
				default:
				}
			}
		}
	}
}
func (s *Scheduler) Once(ctx context.Context) error {
	for _, x := range s.checks() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		v := s.Probe(ctx, s.Config, x)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := s.Sink(s.stamp(v)); e != nil {
			return e
		}
	}
	return nil
}
