// Package runner provides a bounded, non-overlapping, jittered scheduler. A
// storage failure stops scheduling rather than silently dropping measurements.
package runner

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
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

	sources       SourceRuntime
	sourceSink    BatchSink
	sourceWorkers int
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
	return &Scheduler{Config: c, Observer: observer, Workers: workers, Boot: boot, Probe: p, Sink: sink, sourceWorkers: maxSourceWorkers}, nil
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
		if x.Kind != "external" && x.Observer == s.Observer && !x.Disabled && !x.Maintenance {
			r = append(r, x)
		}
	}
	return r
}

// ValidateExecution completes local runtime preflight without starting probes.
// Reload callers must pass this before retiring a running scheduler.
func (s *Scheduler) ValidateExecution() error {
	sources, _ := s.localSources()
	if len(sources) > 0 {
		if s.sources == nil || s.sourceSink == nil || s.Probe == nil {
			return fmt.Errorf("external sources require an explicitly wired source runtime and fan-out sink")
		}
		if s.sourceWorkers < 1 || s.sourceWorkers > maxSourceWorkers {
			return fmt.Errorf("source concurrency must be 1..%d", maxSourceWorkers)
		}
		if e := s.sources.Validate(s.Config); e != nil {
			return e
		}
	}
	return nil
}

// Run is single-owner; do not call Run and Once concurrently on one scheduler.
func (s *Scheduler) Run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	sources, bySource := s.localSources()
	if err := s.ValidateExecution(); err != nil {
		return err
	}
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
	var sourceJobs chan sourceJob
	var sourceDone chan sourceResult
	sourceIdle := 0
	sourceDue := make([]time.Time, len(sources))
	sourceActive := make([]bool, len(sources))
	sourceFailures := make([]int, len(sources))
	if len(sources) > 0 {
		sourceJobs = make(chan sourceJob)
		sourceDone = make(chan sourceResult, s.sourceWorkers)
		sourceIdle = min(s.sourceWorkers, len(sources))
		start := time.Now()
		for i := range sourceDue {
			sourceDue[i] = start.Add(time.Duration(rand.IntN(1000)) * time.Millisecond)
		}
		for range min(s.sourceWorkers, len(sources)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case job, ok := <-sourceJobs:
						if !ok {
							return
						}
						src := sources[job.index]
						observations, failed := s.collectSource(ctx, src, bySource[src.ID])
						select {
						case sourceDone <- sourceResult{index: job.index, observations: observations, failed: failed}:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}
	}
	defer func() { cancel(); wg.Wait() }()
	due := make([]time.Time, len(checks))
	active := make([]bool, len(checks))
	busy := map[string]bool{}
	ready := make([]int, 0, len(checks))
	now := time.Now()
	for i := range due {
		due[i] = now.Add(time.Duration(rand.IntN(1000)) * time.Millisecond)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	key := func(x model.Check) string { return x.Observer + "/" + config.Target(s.Config, x) }
	idle := s.Workers
	// hand gives idle workers the checks that are due, the longest overdue
	// first, so under load each check waits its turn instead of those
	// listed first taking every worker that comes free.
	hand := func(now time.Time) {
		ready = ready[:0]
		for i := range checks {
			if !active[i] && !now.Before(due[i]) {
				ready = append(ready, i)
			}
		}
		slices.SortStableFunc(ready, func(a, b int) int { return due[a].Compare(due[b]) })
		for _, i := range ready {
			if idle == 0 {
				return
			}
			x := checks[i]
			if busy[key(x)] {
				continue
			}
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
			idle--
			active[i] = true
			busy[key(x)] = true
		}
	}
	handSources := func(now time.Time) {
		if len(sources) == 0 || sourceIdle == 0 {
			return
		}
		ready := []int{}
		for i := range sources {
			if !sourceActive[i] && !now.Before(sourceDue[i]) {
				ready = append(ready, i)
			}
		}
		slices.SortStableFunc(ready, func(a, b int) int { return sourceDue[a].Compare(sourceDue[b]) })
		for _, i := range ready {
			if sourceIdle == 0 {
				return
			}
			select {
			case sourceJobs <- sourceJob{index: i}:
			case <-ctx.Done():
				return
			}
			sourceIdle--
			sourceActive[i] = true
		}
	}
	handSources(time.Now())
	for {
		select {
		case <-ctx.Done():
			return nil
		case r := <-done:
			idle++
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
			hand(time.Now())
		case r := <-sourceDone:
			sourceIdle++
			sourceActive[r.index] = false
			if ctx.Err() != nil {
				return nil
			}
			source := sources[r.index]
			if e := s.sendFanout(r.observations); e != nil {
				return e
			}
			delay := time.Duration(source.IntervalSeconds) * time.Second
			if r.failed {
				sourceFailures[r.index]++
				delay = sourceBackoff(delay, sourceFailures[r.index])
			} else {
				sourceFailures[r.index] = 0
				delay = jitter(delay)
			}
			sourceDue[r.index] = time.Now().Add(delay)
			handSources(time.Now())
		case now := <-tick.C:
			hand(now)
			handSources(now)
		}
	}
}
func (s *Scheduler) Once(ctx context.Context) error {
	sources, bySource := s.localSources()
	if len(sources) > 0 {
		if err := s.ValidateExecution(); err != nil {
			return err
		}
		for _, src := range sources {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			observations, _ := s.collectSource(ctx, src, bySource[src.ID])
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e := s.sendFanout(observations); e != nil {
				return e
			}
		}
	}
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
