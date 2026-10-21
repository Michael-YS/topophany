package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCheckDetailShowsSettleConfirmationSamples(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	published := at.Add(-time.Second)
	check := model.Check{
		ID: "edge", Observer: "home", Kind: "mihomo", IntervalSeconds: 10, PolicyThreshold: 1,
		Adapter: model.AdapterOptions{ProviderReference: &model.MihomoProviderReference{URL: "https://publisher.invalid/members.json", Providers: []string{"pool"}, MaxAgeSeconds: 3600}},
	}
	snap := model.Snapshot{
		Topology: model.Config{Checks: []model.Check{check}, Observers: []model.Observer{{ID: "home", Name: "Home"}}},
		Checks: []model.CheckState{{
			ID: "edge", Observer: "home", Health: "degraded", Freshness: "fresh", Contract: "abc",
			Last: &model.Observation{
				SampledAt: at, Outcome: "policy",
				Facts: map[string]string{
					"provider_reference_load_pending": "true",
					"provider_reference_published_at": published.Format(time.RFC3339Nano),
				},
			},
		}},
	}
	var details string
	for _, row := range (View{Snapshot: snap, Now: at}).Rows(Checks) {
		if row.ID == "edge" {
			details = strings.Join(row.Details, "\n")
		}
	}
	if !strings.Contains(details, "Policy confirmation samples: 12") {
		t.Fatalf("detail hid the settle window:\n%s", details)
	}
	if strings.Contains(details, "Policy confirmation samples: 1\n") || strings.HasSuffix(details, "Policy confirmation samples: 1") {
		t.Fatalf("detail showed only the base threshold:\n%s", details)
	}
}
