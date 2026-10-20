package engine

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// The 2026-10-17 acceptance item 4: an API read that succeeds does not turn a
// failing business check green, and an API read that fails does not create
// downtime while the business check passes. The API check is reported evidence
// about the platform, not a request the edge answered.
func TestCloudflareAPIReportDoesNotDecideBusinessHealth(t *testing.T) {
	c := testkit.Config()
	api := model.Check{
		ID: "cf-api", Name: "Cloudflare API", Observer: "home", Service: "web", Kind: "cloudflare",
		IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2,
		Adapter: model.AdapterOptions{TokenEnv: "CF_API_TOKEN", Cloudflare: &model.CloudflareOptions{
			Read:      "tunnel",
			AccountID: "11111111111111111111111111111111",
			TunnelID:  "22222222222222222222222222222222",
		}},
	}
	// Keep one business check on the web service so the aggregate is not
	// diluted by unrelated checks the fixture topology ships.
	c.Checks = []model.Check{c.Checks[0]}
	c.Checks = append(c.Checks, api)
	now := time.Now().UTC()

	apiSample := func(sequence uint64, outcome, code string) model.Observation {
		v := testkit.Observation(c, api.ID, sequence, now.Add(-time.Second), outcome)
		v.Evidence = "reported"
		v.Stage = "adapter_api"
		v.PathEvidence = "cloudflare_api_reported"
		v.Facts = map[string]string{"tunnel_status": "healthy", "peer_text": "withheld_secret_request"}
		if code != "" {
			v.Stage, v.Code = "adapter_api", code
			v.Facts = map[string]string{"peer_text": "withheld_secret_request"}
		}
		return v
	}
	businessSamples := func(id string, sequences []uint64, outcome string) []model.Observation {
		rows := make([]model.Observation, 0, len(sequences))
		for i, seq := range sequences {
			at := now.Add(-time.Duration(len(sequences)-i) * time.Second)
			rows = append(rows, testkit.Observation(c, id, seq, at, outcome))
		}
		return rows
	}

	// The API says the tunnel is healthy while the business check fails: the
	// service is not up. Its business check keeps its own failing state.
	rows := append(businessSamples("home-http", []uint64{1, 2, 3}, "fail"), apiSample(10, "ok", ""))
	s := Build(c, "collector", "", rows, nil, now)
	var business, reported *model.CheckState
	for i := range s.Checks {
		switch s.Checks[i].ID {
		case "home-http":
			business = &s.Checks[i]
		case api.ID:
			reported = &s.Checks[i]
		}
	}
	if business == nil || business.Health != "down" {
		t.Fatalf("business check hidden by the API read: %+v", business)
	}
	if reported == nil || reported.Health == "down" {
		t.Fatalf("API read invented a business failure: %+v", reported)
	}
	if service := findEntity(t, s.Services, "web"); service.Health == "up" {
		t.Fatalf("an API read turned the failing service green: %+v", service)
	}

	// The API read fails with authentication_failed while the business check
	// passes: no downtime and no policy incident from the monitor source.
	rows = append(businessSamples("home-http", []uint64{1, 2, 3}, "ok"), apiSample(10, "error", "authentication_failed"))
	s = Build(c, "collector", "", rows, nil, now)
	for i := range s.Checks {
		if s.Checks[i].ID == api.ID && s.Checks[i].Health == "down" {
			t.Fatalf("a monitor authentication error became a failed check: %+v", s.Checks[i])
		}
	}
	service := findEntity(t, s.Services, "web")
	if service.Health == "down" {
		t.Fatalf("a failing monitor source created business downtime: %+v", service)
	}
	for _, incident := range s.Incidents {
		for _, id := range incident.Checks {
			if id == api.ID {
				t.Fatalf("monitor error opened an incident: %+v", incident)
			}
		}
		if incident.Scope == "service:web" {
			t.Fatalf("monitor error opened a service incident: %+v", incident)
		}
	}
}
