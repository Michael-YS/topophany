package store

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
	"time"
)

func TestLateSampleBeforeCarryDoesNotRewindConfirmation(t *testing.T) {
	c := testkit.Config()
	c.Checks[0].FailureThreshold = 3
	c.Checks[0].RecoveryThreshold = 2
	start := time.Now().UTC().Add(-time.Minute)
	now := start.Add(45 * time.Second)
	rows := []model.Observation{}
	for i := 0; i < 44; i++ {
		outcome := "fail"
		if i >= 3 && i%2 == 1 {
			outcome = "ok"
		}
		rows = append(rows, testkit.Observation(c, "home-http", uint64(i+1), start.Add(time.Duration(i)*time.Second), outcome))
	}
	states := []model.CheckState{}
	for _, late := range []bool{false, true} {
		s := openTest(t, t.TempDir(), Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Now: func() time.Time { return now }})
		for i, v := range rows {
			if late && i == 1 {
				continue
			}
			if err := s.Put(batch(c, v)); err != nil {
				t.Fatal(err)
			}
		}
		if late {
			if err := s.Put(batch(c, rows[1])); err != nil {
				t.Fatal(err)
			}
		}
		raw, _ := s.View()
		if len(raw) != len(rows) {
			t.Fatal("missing history")
		}
		states = append(states, judged(t, s, c, "home-http", now))
	}
	t.Logf("same observation set: ordered=%s, late=%s", states[0].Health, states[1].Health)
	if states[0].Health != "degraded" || states[1].Health != "up" {
		t.Fatalf("unexpected watermark semantics: ordered=%s late=%s", states[0].Health, states[1].Health)
	}
	if states[0].Last.ID != states[1].Last.ID {
		t.Fatal("late sample replaced latest evidence")
	}
}
