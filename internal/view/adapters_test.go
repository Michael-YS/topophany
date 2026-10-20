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

func TestAdapterReadFailureAndKnownPolicyViolationStayDistinct(t *testing.T) {
	check := model.Check{Kind: "mihomo", Adapter: model.AdapterOptions{ExpectedMode: "rule"}}
	state := model.CheckState{Freshness: "fresh", Last: &model.Observation{Evidence: "reported", Stage: "adapter", Outcome: "error"}}
	if text := strings.Join(adapterDetails(check, state), "\n"); strings.Contains(text, "Runtime read: available") {
		t.Fatal(text)
	}
	state.Last.Stage = "provider_reference"
	state.Last.Facts = map[string]string{"mode": "global"}
	if text := strings.Join(adapterDetails(check, state), "\n"); !strings.Contains(text, "Policy check: violated") || !strings.Contains(text, "Runtime read: available") {
		t.Fatal(text)
	}
}

func TestCloudflaredAdapterDetails(t *testing.T) {
	four := 4
	check := model.Check{Kind: "cloudflared", Adapter: model.AdapterOptions{Cloudflared: &model.CloudflaredOptions{ExpectedConnections: &four}}}
	for _, tc := range []struct {
		name                 string
		outcome, code, stage string
		facts                map[string]string
		fresh                bool
		runtime, policy      string
	}{
		{name: "established", outcome: "ok", stage: "adapter", facts: map[string]string{"ready_connections": "4", "ha_connections": "4"}, fresh: true, runtime: "available", policy: "matched"},
		{name: "connections below", outcome: "policy", code: "connections_below_expected", stage: "adapter", facts: map[string]string{"ready_connections": "2"}, fresh: true, runtime: "available", policy: "violated"},
		{name: "colo outside", outcome: "policy", code: "colo_outside_expected", stage: "adapter", facts: map[string]string{"ready_connections": "4"}, fresh: true, runtime: "available", policy: "violated"},
		{name: "no session", outcome: "fail", code: "tunnel_not_ready", stage: "adapter", facts: map[string]string{"ready_connections": "0"}, fresh: true, runtime: "available", policy: "unverified"},
		{name: "metrics unavailable", outcome: "error", code: "metrics_unavailable", stage: "adapter", fresh: true, runtime: "unverified", policy: "unverified"},
		{name: "stale", outcome: "ok", stage: "adapter", facts: map[string]string{"ready_connections": "4"}, runtime: "unverified", policy: "unverified"},
		{name: "fact without stage", outcome: "ok", facts: map[string]string{"ha_connections": "4"}, fresh: true, runtime: "available", policy: "matched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := model.CheckState{Freshness: "fresh", Last: &model.Observation{Evidence: "reported", Outcome: tc.outcome, Code: tc.code, Stage: tc.stage, Facts: tc.facts}}
			if !tc.fresh {
				state.Freshness = "stale"
			}
			text := strings.Join(adapterDetails(check, state), "\n")
			for _, want := range []string{"Runtime read: " + tc.runtime, "Policy check: " + tc.policy, "Business reachability: unmeasured"} {
				if !strings.Contains(text, want) {
					t.Fatal(want, text)
				}
			}
		})
	}
	if adapterDetails(model.Check{Kind: "http"}, model.CheckState{}) != nil {
		t.Fatal("application probes relabeled as software telemetry")
	}
}
