package api

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A cloudflared check reads the connector's local runtime, so the collector
// admits it only as reported evidence: a worker cannot pass it off as a
// measured remote response.
func TestCloudflaredIsAdmittedAsReportedEvidence(t *testing.T) {
	c := testkit.Config()
	c.Endpoints = append(c.Endpoints, model.Endpoint{ID: "cf-metrics", Node: "home", Address: "http://127.0.0.1:20241", Network: "loopback", Observer: "home"})
	c.Checks = append(c.Checks, model.Check{ID: "tunnel", Name: "cloudflared", Observer: "home", Endpoint: "cf-metrics", Kind: "cloudflared", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2})
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := Auth{Schema: 1, Tokens: []Principal{{secure.Hash(viewerToken), "viewer", ""}, {secure.Hash(workerToken), "worker", "home"}}}
	srv := New(c, "test", s, a)
	srv.Now = func() time.Time { return now }

	v := testkit.Observation(c, "tunnel", 1, now.Add(-time.Minute), "ok")
	if v.Evidence != "measured" {
		t.Fatal("fixture is not a measured sample")
	}
	batch := func(v model.Observation) model.Batch {
		return model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}}
	}
	if code, status := srv.ValidateBatch(batch(v), "home"); code != "evidence_kind_mismatch" || status != 400 {
		t.Fatalf("measured evidence accepted: %s %d", code, status)
	}

	v.Evidence = "reported"
	v.Stage = "adapter"
	v.PathEvidence = "local_connector_runtime_reported"
	v.Facts = map[string]string{
		"ready_connections": "4",
		"ha_connections":    "4",
		"connection_colos":  "hkg,sin",
		"connector_version": "2026.10.0",
	}
	if code, status := srv.ValidateBatch(batch(v), "home"); code != "" || status != 0 {
		t.Fatalf("reported evidence refused: %s %d", code, status)
	}
}
