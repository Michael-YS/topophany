package store

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// judged is a check's state as a collector's snapshot shows it.
func judged(t *testing.T, s *Store, c model.Config, id string, now time.Time) model.CheckState {
	t.Helper()
	rows, carries, beats := s.StateView()
	for _, st := range engine.BuildCarried(c, "a", "", rows, carries, beats, now).Checks {
		if st.ID == id {
			return st
		}
	}
	t.Fatal("no state for", id)
	return model.CheckState{}
}

// flapping is a check confirmed down after three failures that has
// alternated since, ending with a pass: it never passed twice in a row,
// so with a recovery threshold of two it is still recovering.
func flapping(c model.Config, id string, n int, start time.Time, gap time.Duration) []model.Observation {
	rows := []model.Observation{}
	for i := 0; i < n; i++ {
		outcome := "fail"
		if i >= 3 && i%2 == 0 {
			outcome = "ok"
		}
		rows = append(rows, testkit.Observation(c, id, uint64(i+1), start.Add(time.Duration(i)*gap), outcome))
	}
	return rows
}

func flappingConfig() (model.Config, model.Check) {
	c := testkit.Config()
	c.Checks[0].FailureThreshold, c.Checks[0].RecoveryThreshold = 3, 2
	return c, c.Checks[0]
}

// The samples that confirmed a failure can leave the history and the
// state tail, whether pushed out by the history's capacity or aged out
// by retention; the failure they confirmed stays until the check
// recovers, through checkpoints and restarts.
func TestConfirmedFailureOutlivesTheSamplesThatConfirmedIt(t *testing.T) {
	c, x := flappingConfig()
	now := time.Now().UTC()
	clock := now
	for name, o := range map[string]Options{
		"history capacity": {MaxHistory: 10},
		"retention":        {Retention: time.Minute, Now: func() time.Time { return clock }},
		"checkpoints":      {MaxHistory: 10, CheckpointBytes: 1024},
	} {
		t.Run(name, func(t *testing.T) {
			clock = now
			dir := t.TempDir()
			s := openTest(t, dir, o)
			rows := flapping(c, x.ID, 181, now.Add(-30*time.Minute), 10*time.Second)
			for _, v := range rows {
				clock = v.SampledAt
				if e := s.Put(batch(c, v)); e != nil {
					t.Fatal(e)
				}
			}
			at := rows[len(rows)-1].SampledAt.Add(time.Second)
			clock = at
			if held, _ := s.View(); len(held) >= len(rows)-tailLength {
				t.Fatalf("only %d of %d samples should be held", len(held), len(rows))
			}
			want := engine.Evaluate(x, rows, at)
			if want.Health != "degraded" || want.Reason != "recovering; waiting for consecutive successes" {
				t.Fatal("fixture is not a check still recovering", want)
			}
			if got := judged(t, s, c, x.ID, at); got.Health != want.Health || got.Reason != want.Reason || !got.Since.Equal(want.Since) {
				t.Fatal("trimming the samples that confirmed it recovered it", got)
			}
			s.Close()
			s = openTest(t, dir, o)
			if got := judged(t, s, c, x.ID, at); got.Health != want.Health || got.Reason != want.Reason {
				t.Fatal("a restart recovered it", got)
			}
			// Two passes in a row recover it, as they would have before.
			for i, v := range []string{"ok", "ok"} {
				next := testkit.Observation(c, x.ID, uint64(len(rows)+i+1), at.Add(time.Duration(i)*10*time.Second), v)
				clock = next.SampledAt
				if e := s.Put(batch(c, next)); e != nil {
					t.Fatal(e)
				}
			}
			if got := judged(t, s, c, x.ID, clock.Add(time.Second)); got.Health != "up" {
				t.Fatal("it did not recover after passing twice", got)
			}
		})
	}
}

// What samples left behind belongs to their check under one contract: a
// check that now measures something else starts afresh.
func TestCarriesFollowTheContract(t *testing.T) {
	c, x := flappingConfig()
	s := openTest(t, t.TempDir(), Options{MaxHistory: 10})
	now := time.Now().UTC()
	rows := flapping(c, x.ID, 81, now.Add(-time.Hour), 10*time.Second)
	for _, v := range rows {
		if e := s.Put(batch(c, v)); e != nil {
			t.Fatal(e)
		}
	}
	at := rows[len(rows)-1].SampledAt.Add(time.Second)
	if st := judged(t, s, c, x.ID, at); st.Health != "degraded" {
		t.Fatal(st)
	}
	edited := c
	edited.Checks = append([]model.Check(nil), c.Checks...)
	edited.Checks[0].Target = "https://other.example.invalid/"
	if st := judged(t, s, edited, x.ID, at); st.Health != "unknown" || st.Reason != "not yet measured" {
		t.Fatal("a carry crossed into another contract", st)
	}
}
