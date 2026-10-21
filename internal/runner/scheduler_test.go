package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnceScopeIdentityAndExclusions(t *testing.T) {
	c := testkit.Config()
	c.Checks[1].Disabled = true
	c.Checks[3].Maintenance = true
	var got []model.Observation
	p := func(ctx context.Context, c model.Config, x model.Check) model.Observation {
		return model.Observation{Check: x.ID, Observer: "forged", Project: "forged", ReceivedAt: time.Now(), SampledAt: time.Now(), Outcome: "ok", Evidence: "measured"}
	}
	s, e := New(c, "home", 2, p, func(v model.Observation) error { got = append(got, v); return nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Once(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(got) != 3 {
		t.Fatal(len(got))
	}
	for i, v := range got {
		if v.Observer != "home" || v.Project != c.Project || v.Contract != config.Contracts(c)[v.Check] || v.Revision != "" || v.Sequence != uint64(i+1) || !v.ReceivedAt.IsZero() || v.Boot != s.Boot {
			t.Fatal(v)
		}
	}
	old := s.Boot
	s2, e := New(c, "home", 1, p, func(v model.Observation) error { return nil })
	if e != nil || s2.Boot == old {
		t.Fatal("boot identities reused")
	}
}
func TestSchedulerConcurrencyTargetExclusionAndCancellation(t *testing.T) {
	c := testkit.Config()
	c.Checks = nil
	for i, id := range []string{"a", "b", "c", "d"} {
		target := "127.0.0.1:1"
		if i >= 2 {
			target = "127.0.0.1:2"
		}
		c.Checks = append(c.Checks, model.Check{ID: id, Observer: "home", Endpoint: "dmit-ssh", Target: target, Kind: "tcp", IntervalSeconds: 1, TimeoutSeconds: 1, StaleSeconds: 3, FailureThreshold: 1, RecoveryThreshold: 1})
	}
	var mu sync.Mutex
	active, peak, calls := 0, 0, 0
	byTarget := map[string]int{}
	ctx, cancel := context.WithTimeout(context.Background(), 2400*time.Millisecond)
	defer cancel()
	p := func(ctx context.Context, c model.Config, x model.Check) model.Observation {
		mu.Lock()
		active++
		calls++
		if active > peak {
			peak = active
		}
		byTarget[x.Target]++
		if active > 2 || byTarget[x.Target] > 1 {
			t.Error("scheduler exceeded global/target budget")
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(180 * time.Millisecond):
		}
		mu.Lock()
		active--
		byTarget[x.Target]--
		mu.Unlock()
		return model.Observation{Check: x.ID, SampledAt: time.Now(), Outcome: "ok"}
	}
	s, e := New(c, "home", 2, p, func(v model.Observation) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e = s.Run(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if active != 0 || calls < 2 || peak > 2 || time.Since(start) > 3500*time.Millisecond {
		t.Fatal(active, calls, peak, time.Since(start))
	}
}
func TestEveryCheckTakesItsTurnUnderSustainedLoad(t *testing.T) {
	// Twelve checks a second, each taking 120ms, on one worker: more
	// work than it can do, so checks wait, but every one in turn.
	c := testkit.Config()
	c.Checks = nil
	for i := 0; i < 12; i++ {
		c.Checks = append(c.Checks, model.Check{ID: fmt.Sprintf("c%02d", i), Observer: "home", Endpoint: "dmit-ssh", Target: fmt.Sprintf("127.0.0.1:%d", i+1), Kind: "tcp", IntervalSeconds: 1, TimeoutSeconds: 1, StaleSeconds: 3, FailureThreshold: 1, RecoveryThreshold: 1})
	}
	var mu sync.Mutex
	starts := map[string][]time.Time{}
	p := func(ctx context.Context, c model.Config, x model.Check) model.Observation {
		mu.Lock()
		starts[x.ID] = append(starts[x.ID], time.Now())
		mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(120 * time.Millisecond):
		}
		return model.Observation{Check: x.ID, SampledAt: time.Now(), Outcome: "ok"}
	}
	s, e := New(c, "home", 1, p, func(v model.Observation) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if e = s.Run(ctx); e != nil {
		t.Fatal(e)
	}
	end := time.Now()
	mu.Lock()
	defer mu.Unlock()
	// A round of all twelve takes 1.44s; waiting longer than 2.5s for a
	// turn means another check was served out of turn.
	for _, x := range c.Checks {
		at := starts[x.ID]
		if len(at) < 2 {
			t.Fatalf("%s started %d times in %v", x.ID, len(at), end.Sub(begin))
		}
		last := begin
		for _, v := range append(at, end) {
			if wait := v.Sub(last); wait > 2500*time.Millisecond {
				t.Fatalf("%s waited %v for a turn", x.ID, wait)
			}
			last = v
		}
	}
}
func TestSinkFailureStopsWithoutLosingError(t *testing.T) {
	boom := errors.New("durable storage full")
	p := func(ctx context.Context, c model.Config, x model.Check) model.Observation {
		return model.Observation{Check: x.ID, SampledAt: time.Now()}
	}
	s, e := New(testkit.Config(), "home", 1, p, func(v model.Observation) error { return boom })
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Once(context.Background()); !errors.Is(e, boom) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = s.Run(ctx); !errors.Is(e, boom) {
		t.Fatal(e)
	}
}
func TestCancellationAndInvalidScheduler(t *testing.T) {
	c := testkit.Config()
	p := func(ctx context.Context, c model.Config, x model.Check) model.Observation {
		t.Error("probe ran after cancellation")
		return model.Observation{}
	}
	sink := func(v model.Observation) error { return nil }
	for _, n := range []int{0, 33} {
		if _, e := New(c, "home", n, p, sink); e == nil {
			t.Fatal(n)
		}
	}
	if _, e := New(c, "missing", 1, p, sink); e == nil {
		t.Fatal("unknown observer accepted")
	}
	if _, e := New(c, "home", 1, nil, sink); e == nil {
		t.Fatal("nil probe")
	}
	s, e := New(c, "home", 1, p, sink)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = s.Once(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = s.Run(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestCancelledProbeIsNotDelivered(t *testing.T) {
	for _, name := range []string{"once", "run"} {
		t.Run(name, func(t *testing.T) {
			c := testkit.Config()
			started := make(chan struct{}, 1)
			p := func(ctx context.Context, c model.Config, x model.Check) model.Observation {
				select {
				case started <- struct{}{}:
				default:
				}
				<-ctx.Done()
				return model.Observation{Check: x.ID, SampledAt: time.Now(), Outcome: "fail", Code: "timeout"}
			}
			var sunk atomic.Int32
			s, err := New(c, "home", 1, p, func(model.Observation) error {
				sunk.Add(1)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if name == "once" {
					done <- s.Once(ctx)
				} else {
					done <- s.Run(ctx)
				}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("probe did not start")
			}
			cancel()
			select {
			case err := <-done:
				if name == "once" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if name == "run" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("scheduler did not stop")
			}
			if sunk.Load() != 0 {
				t.Fatalf("cancelled probe was delivered %d times", sunk.Load())
			}
		})
	}
}
