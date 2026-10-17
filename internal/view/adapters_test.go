package view

import (
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestAdapterStatusFacetsPreserveEvidenceLimits(t *testing.T) {
	check := model.Check{Kind: "mihomo", Adapter: model.AdapterOptions{ExpectedMode: "rule"}}
	for _, tc := range []struct{ outcome, fresh, runtime, policy string }{
		{"ok", "fresh", "available", "matched"},
		{"policy", "fresh", "available", "violated"},
		{"error", "fresh", "available", "unverified"},
		{"ok", "stale", "unverified", "unverified"},
	} {
		state := model.CheckState{Freshness: tc.fresh, Last: &model.Observation{Evidence: "reported", Outcome: tc.outcome, Stage: "adapter", Facts: map[string]string{"mode": "rule"}}}
		text := strings.Join(adapterDetails(check, state), "\n")
		for _, want := range []string{"Runtime read: " + tc.runtime, "Policy check: " + tc.policy, "Business reachability: unmeasured"} {
			if !strings.Contains(text, want) {
				t.Fatal(want, text)
			}
		}
	}
	if adapterDetails(model.Check{Kind: "http"}, model.CheckState{}) != nil {
		t.Fatal("application probes relabeled as software telemetry")
	}
}
