package engine

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Samples folded into a carry judge a check as they would if they were
// all still held, whatever its thresholds, wherever the cut falls.
func TestCarryJudgesAsTheSamplesItHolds(t *testing.T) {
	c := testkit.Config()
	r := rand.New(rand.NewPCG(1, 2))
	outcomes := []string{"ok", "ok", "ok", "fail", "fail", "fail", "policy", "error"}
	for round := 0; round < 2000; round++ {
		x := c.Checks[0]
		x.FailureThreshold, x.RecoveryThreshold = 1+r.IntN(20), 1+r.IntN(20)
		x.StaleSeconds = 60
		at := time.Now().Add(-time.Hour)
		rows := []model.Observation{}
		for i := 0; i < 1+r.IntN(120); i++ {
			at = at.Add(time.Duration(5+r.IntN(30)) * time.Second)
			if r.IntN(40) == 0 {
				at = at.Add(2 * time.Minute) // longer than stale
			}
			rows = append(rows, testkit.Observation(c, x.ID, uint64(i+1), at, outcomes[r.IntN(len(outcomes))]))
		}
		now := at.Add(time.Second)
		cut := r.IntN(len(rows))
		var k Carry
		for _, v := range rows[:cut] {
			k.Fold(v, time.Duration(x.StaleSeconds)*time.Second)
		}
		// The carried samples may be passed again, as a store passes the
		// history it still holds; they are not counted twice.
		held := rows[r.IntN(cut+1):]
		want, got := Evaluate(x, rows, now), EvaluateCarried(x, &k, held, now)
		if want.Health != got.Health || want.Reason != got.Reason || !want.Since.Equal(got.Since) || want.Failures != got.Failures || want.Successes != got.Successes || want.Freshness != got.Freshness {
			t.Fatalf("round %d, thresholds %d/%d, cut %d of %d: carried %+v, held %+v", round, x.FailureThreshold, x.RecoveryThreshold, cut, len(rows), got, want)
		}
	}
}

// A check that failed, was confirmed down and has flapped since, never
// passing twice in a row, is still recovering however long ago it failed.
func TestCarryKeepsAnIncidentThatNeverRecovered(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	x.FailureThreshold, x.RecoveryThreshold = 3, 2
	at := time.Now().Add(-time.Hour)
	var k Carry
	for i := 0; i < 1000; i++ {
		outcome := "fail"
		if i >= 3 && i%2 == 0 {
			outcome = "ok"
		}
		k.Fold(testkit.Observation(c, x.ID, uint64(i+1), at.Add(time.Duration(i)*time.Second), outcome), time.Minute)
	}
	last := testkit.Observation(c, x.ID, 1001, at.Add(1000*time.Second), "ok")
	if s := EvaluateCarried(x, &k, []model.Observation{last}, last.SampledAt); s.Health != "degraded" || s.Reason != "recovering; waiting for consecutive successes" {
		t.Fatal("a flapping check recovered without passing twice", s)
	}
	x.RecoveryThreshold = 1
	if s := EvaluateCarried(x, &k, []model.Observation{last}, last.SampledAt); s.Health != "up" {
		t.Fatal("a lower recovery threshold did not apply to what was carried", s)
	}
}
