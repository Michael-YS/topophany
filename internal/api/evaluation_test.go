package api

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestPreparedEvaluationReusesEvidenceAndInvalidatesOnWriteAndReload(t *testing.T) {
	s, _, b := fixture(t)
	now := s.Now()
	b.Observations[0].SampledAt = now
	if err := s.Store.Put(b); err != nil {
		t.Fatal(err)
	}
	first := s.MonitorSnapshot(now)
	prepared := s.evaluation
	expired := s.MonitorSnapshot(now.Add(24 * time.Hour))
	if s.evaluation != prepared || first.Checks[0].Freshness != "fresh" || expired.Checks[0].Freshness != "stale" {
		t.Fatal("time frozen or unchanged evidence rebuilt")
	}
	if _, _, err := s.Observe(now, true); err != nil {
		t.Fatal(err)
	}
	if s.evaluation != prepared {
		t.Fatal("ledger did not reuse monitor evaluation")
	}
	b.Observations[0] = testkit.Observation(s.Config, "home-http", 2, now.Add(time.Second), "fail")
	if err := s.Store.Put(b); err != nil {
		t.Fatal(err)
	}
	s.MonitorSnapshot(now.Add(time.Second))
	if s.evaluation == prepared {
		t.Fatal("new evidence did not invalidate evaluation")
	}
	prepared = s.evaluation
	next := config.Clone(s.Config)
	next.Checks[0].Disabled = true
	s.Reload(next, s.Auth)
	if got := s.MonitorSnapshot(now); s.evaluation == prepared || got.Checks[0].Freshness != "paused" {
		t.Fatal("reload retained old judgment")
	}
}
