package api

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A cloudflare API read is the platform's own statement, so the collector
// admits it only as reported evidence: a worker cannot pass it off as a
// measured remote response.
func TestCloudflareIsAdmittedAsReportedEvidence(t *testing.T) {
	c := testkit.Config()
	c.Checks = append(c.Checks, model.Check{
		ID: "cf-api", Name: "Cloudflare tunnel", Observer: "home", Kind: "cloudflare",
		IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2,
		Adapter: model.AdapterOptions{
			TokenEnv: "CF_API_TOKEN",
			Cloudflare: &model.CloudflareOptions{
				Read:      "tunnel",
				AccountID: "11111111111111111111111111111111",
				TunnelID:  "22222222222222222222222222222222",
			},
		},
	})
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

	v := testkit.Observation(c, "cf-api", 1, now.Add(-time.Minute), "ok")
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
	v.Stage = "adapter_api"
	v.PathEvidence = cloudflare.PathEvidence
	v.Facts = map[string]string{
		"tunnel_status":            "healthy",
		"tunnel_connections":       "3",
		"tunnel_pending_reconnect": "0",
		"tunnel_colos":             "HKG,SIN",
		"peer_text":                "withheld_secret_request",
	}
	if code, status := srv.ValidateBatch(batch(v), "home"); code != "" || status != 0 {
		t.Fatalf("reported evidence refused: %s %d", code, status)
	}
}
