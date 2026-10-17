package view

import "github.com/WhitenWhiten/topophany/internal/model"

// Software telemetry and a policy assertion answer different questions from an
// application response. Keep those answers visible without upgrading evidence.
func adapterDetails(check model.Check, state model.CheckState) []string {
	if check.Kind != "mihomo" && check.Kind != "tailscale" && check.Kind != "wireguard" {
		return nil
	}
	runtime, policy := "unverified", "not configured"
	configured := check.Kind == "mihomo" && (check.Adapter.ExpectedMode != "" || len(check.Adapter.ExpectedProviders) > 0 || check.Adapter.ProviderReference != nil)
	configured = configured || check.Kind == "wireguard" && (len(check.Adapter.ExpectedAllowedIPs) > 0 || check.Adapter.ExpectedKeepaliveSeconds != nil)
	if configured {
		policy = "unverified"
	}
	if last := state.Last; last != nil && state.Freshness == "fresh" {
		if last.Evidence == "reported" && (last.Stage == "adapter" || last.Facts["mode"] != "" || last.Facts["backend_state"] != "" || last.Facts["rx_bytes"] != "") {
			runtime = "available"
		}
		if last.Outcome == "policy" {
			policy = "violated"
		} else if configured && last.Outcome == "ok" {
			policy = "matched"
		}
	}
	return []string{"Runtime read: " + runtime, "Policy check: " + policy, "Business reachability: unmeasured; use a separate application probe"}
}
