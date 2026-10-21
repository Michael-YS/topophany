package api

import (
	"github.com/WhitenWhiten/topophany/internal/model"
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

func TestHeartbeatUpdatesLivenessWithoutRefoldingChecks(t *testing.T) {
	s, _, b := fixture(t)
	now := s.Now()
	b.Observations[0].SampledAt = now.Add(-time.Minute)
	if err := s.Store.Put(b); err != nil {
		t.Fatal(err)
	}
	s.MonitorSnapshot(now)
	evidenceVersion := s.evaluationEvidenceVersion
	h := model.Heartbeat{Observer: "home", Boot: "next", SampledAt: now, Contracts: config.ObserverContracts(s.Config, "home")}
	if err := s.Store.Put(model.Batch{Schema: model.BatchSchema, Project: b.Project, Revision: b.Revision, Heartbeat: &h}); err != nil {
		t.Fatal(err)
	}
	snap := s.MonitorSnapshot(now)
	if s.evaluationEvidenceVersion != evidenceVersion {
		t.Fatal("heartbeat invalidated observation evidence")
	}
	rows, carries, beats, _, ev := s.Store.EvaluationView(evidenceVersion, false)
	if rows != nil || carries != nil || ev != evidenceVersion || !beats["home"].SampledAt.Equal(now) {
		t.Fatal("heartbeat capture scanned history")
	}
	for _, o := range snap.Observers {
		if o.ID == "home" && (!o.LastSeen.Equal(now) || o.Contracts != "current") {
			t.Fatal("heartbeat liveness stale", o)
		}
	}
	// Heartbeats from the future must become ineligible again on rollback.
	rollback := s.MonitorSnapshot(now.Add(-20 * time.Second))
	for _, o := range rollback.Observers {
		if o.ID == "home" && !o.LastSeen.Equal(b.Observations[0].SampledAt) {
			t.Fatal("future heartbeat froze last seen", o)
		}
	}
}

func TestSnapshotAvailabilityIsLazyByView(t *testing.T) {
	s, _, _ := fixture(t)
	g := s.generation()
	if _, status, code := s.cachedSnapshot(g, "home", "viewer"); status != 200 {
		t.Fatal(status, code)
	}
	want := map[string]bool{}
	for _, c := range s.Config.Checks {
		if c.Observer == "home" {
			want[c.ID] = true
		}
	}
	if len(want) == 0 || len(want) == len(s.Config.Checks) {
		t.Fatal("fixture needs scoped checks")
	}
	if len(s.snapshotAvailable) != len(want) {
		t.Fatal("read rollups outside requested view", len(s.snapshotAvailable))
	}
	for id := range s.snapshotAvailable {
		if !want[id] {
			t.Fatal("unrequested rollup", id)
		}
	}
	if _, status, code := s.cachedSnapshot(g, "", "viewer"); status != 200 {
		t.Fatal(status, code)
	}
	if len(s.snapshotAvailable) != len(s.Config.Checks) {
		t.Fatal("global view omitted rollups")
	}
}
