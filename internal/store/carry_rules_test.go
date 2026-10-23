package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestCarryRuleChangesReplayAvailableEvidence(t *testing.T) {
	for _, oldStale := range []int{60, 120} {
		for _, limit := range []int{1, 1000} {
			for _, restart := range []bool{false, true} {
				t.Run(fmt.Sprintf("stale=%d/history=%d/restart=%v", oldStale, limit, restart), func(t *testing.T) {
					c := testkit.Config()
					c.Checks[0].StaleSeconds = oldStale
					c.Checks[0].FailureThreshold = 3
					c.Checks[0].RecoveryThreshold = 2
					start := time.Now().UTC().Add(-10 * time.Minute)
					now := start.Add(386 * time.Second)
					o := Options{MaxHistory: limit, Now: func() time.Time { return now }}.WithTopology(c)
					dir := t.TempDir()
					s := openTest(t, dir, o)
					for i := 0; i < 44; i++ {
						sec := i * 90
						outcome := "fail"
						if i >= 3 {
							sec = 185 + (i-3)*5
							if i%2 == 1 {
								outcome = "ok"
							}
						}
						v := testkit.Observation(c, "home-http", uint64(i+1), start.Add(time.Duration(sec)*time.Second), outcome)
						if err := s.Put(batch(c, v)); err != nil {
							t.Fatal(err)
						}
					}
					if len(s.carries) != 1 {
						t.Fatal("fixture did not fold a prefix")
					}
					available := s.allRows()
					contract := config.Contracts(c)["home-http"]
					c.Checks[0].StaleSeconds = 180 - oldStale
					if config.Contracts(c)["home-http"] != contract {
						t.Fatal("rule edit changed measurement identity")
					}
					if restart {
						if err := s.Checkpoint(); err != nil {
							t.Fatal(err)
						}
						s.Close()
						s = openTest(t, dir, Options{MaxHistory: limit, Now: func() time.Time { return now }}.WithTopology(c))
					} else {
						s.SetCurrent(config.Revision(c), config.Contracts(c), config.Stale(c))
					}
					want := engine.Evaluate(c.Checks[0], available, now)
					got := judged(t, s, c, "home-http", now)
					if got.Health != want.Health || got.Reason != want.Reason {
						t.Fatalf("got %s (%s), retained evidence says %s (%s)", got.Health, got.Reason, want.Health, want.Reason)
					}
				})
			}
		}
	}
}

func TestCarryCheckpointPreservesCompatibleRules(t *testing.T) {
	c, x := flappingConfig()
	dir := t.TempDir()
	now := time.Now().UTC()
	o := Options{MaxHistory: 1, Now: func() time.Time { return now }}.WithTopology(c)
	s := openTest(t, dir, o)
	for _, v := range flapping(c, x.ID, 81, now.Add(-time.Minute), time.Second) {
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, dir, o)
	if got := judged(t, s, c, x.ID, now.Add(21*time.Second)); got.Health != "degraded" {
		t.Fatal("restart lost compatible confirmation", got)
	}
	c.Checks[0].Name = "Renamed"
	c.Checks[0].FailureThreshold = 2
	s.SetCurrent(config.Revision(c), config.Contracts(c), config.Stale(c))
	if got := judged(t, s, c, x.ID, now.Add(21*time.Second)); got.Health != "degraded" {
		t.Fatal("presentation or supported threshold edit lost confirmation", got)
	}
}

func TestLegacyCarryReconfirmsWithoutUnknownRules(t *testing.T) {
	c, x := flappingConfig()
	dir := t.TempDir()
	now := time.Now().UTC()
	o := Options{MaxHistory: 1, Now: func() time.Time { return now }}.WithTopology(c)
	s := openTest(t, dir, o)
	for _, v := range flapping(c, x.ID, 81, now.Add(-time.Minute), time.Second) {
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	available := s.allRows()
	for key, k := range s.carries {
		k.RulesVersion = 0
		k.Stale = 0
		s.carries[key] = k
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, dir, o)
	at := now.Add(21 * time.Second)
	want := engine.Evaluate(x, available, at)
	got := judged(t, s, c, x.ID, at)
	if got.Health != want.Health {
		t.Fatalf("legacy rules carried unknown confirmation: got %s want %s", got.Health, want.Health)
	}
}
