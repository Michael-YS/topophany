package monitor

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestLocalThresholdCarryAndContractReload(t *testing.T) {
	now := time.Now()
	c := model.Config{Schema: 1, Project: "test", Observers: []model.Observer{{ID: "local", StaleSeconds: 30}}, Checks: []model.Check{{ID: "host", Kind: "host", Observer: "local", StaleSeconds: 30, FailureThreshold: 2, RecoveryThreshold: 2}}}
	l := NewLocal(c, "local")
	sample := func(id, outcome string, at time.Time) {
		l.Record(model.Observation{ID: id, Check: "host", Observer: "local", Project: c.Project, Contract: config.Contract(c, c.Checks[0]), SampledAt: at, Outcome: outcome, Evidence: "reported"})
	}
	sample("1", "fail", now)
	if s := l.Snapshot(now); len(s.Checks) != 1 || s.Checks[0].Health != "degraded" {
		t.Fatal(s.Checks)
	}
	sample("2", "fail", now.Add(time.Second))
	if s := l.Snapshot(now.Add(time.Second)); s.Checks[0].Health != "down" {
		t.Fatal(s.Checks)
	}
	sample("3", "ok", now.Add(2*time.Second))
	if s := l.Snapshot(now.Add(2 * time.Second)); s.Checks[0].Health != "degraded" {
		t.Fatal(s.Checks)
	}
	sample("4", "ok", now.Add(3*time.Second))
	if s := l.Snapshot(now.Add(3 * time.Second)); s.Checks[0].Health != "up" {
		t.Fatal(s.Checks)
	}
	c.Checks[0].TimeoutSeconds = 8
	l.Reload(c)
	if s := l.Snapshot(now.Add(4 * time.Second)); s.Checks[0].Freshness != "never" {
		t.Fatal("changed contract retained evidence", s.Checks)
	}
}
