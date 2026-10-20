package view

import "github.com/WhitenWhiten/topophany/internal/model"

// Software telemetry and a policy assertion answer different questions from an
// application response. Keep those answers visible without upgrading evidence.
func adapterDetails(check model.Check, state model.CheckState) []string {
	if check.Kind != "mihomo" && check.Kind != "tailscale" && check.Kind != "wireguard" && check.Kind != "cloudflared" && check.Kind != "cloudflare" {
		return nil
	}
	runtime, policy := "unverified", "not configured"
	configured := check.Kind == "mihomo" && (check.Adapter.ExpectedTUN != nil || check.Adapter.ExpectedMode != "" || len(check.Adapter.ExpectedProviders) > 0 || check.Adapter.ProviderReference != nil)
	configured = configured || check.Kind == "wireguard" && (len(check.Adapter.ExpectedAllowedIPs) > 0 || check.Adapter.ExpectedKeepaliveSeconds != nil)
	configured = configured || check.Kind == "cloudflared" && check.Adapter.Cloudflared != nil && (check.Adapter.Cloudflared.ExpectedConnections != nil || len(check.Adapter.Cloudflared.ExpectedColos) > 0)
	configured = configured || check.Kind == "cloudflare" && cloudflarePolicyConfigured(check.Adapter.Cloudflare)
	if configured {
		policy = "unverified"
	}
	if last := state.Last; last != nil && state.Freshness == "fresh" {
		if last.Evidence == "reported" && ((last.Stage == "adapter" || last.Stage == "adapter_api") && last.Outcome != "error" || last.Facts["mode"] != "" || last.Facts["backend_state"] != "" || last.Facts["rx_bytes"] != "" || last.Facts["ready_connections"] != "" || last.Facts["ha_connections"] != "" || last.Facts["tunnel_status"] != "" || last.Facts["record_count"] != "" || last.Facts["pool_healthy"] != "") {
			runtime = "available"
		}
		if policyViolation(check, last) {
			policy = "violated"
		} else if configured && last.Outcome == "ok" {
			policy = "matched"
		}
	}
	return []string{"Runtime read: " + runtime, "Policy check: " + policy, "Business reachability: unmeasured; use a separate application probe"}
}

// cloudflarePolicyConfigured reports whether the API read declares any state
// assertion. Without one the adapter still reports facts, but there is no
// policy to match or violate.
func cloudflarePolicyConfigured(options *model.CloudflareOptions) bool {
	return options != nil && (options.ExpectedConnections != nil || len(options.ExpectedColos) > 0 || options.ExpectedProxied != nil || options.ExpectedContent != "" || len(options.ExpectedOrigins) > 0)
}

func policyViolation(check model.Check, last *model.Observation) bool {
	if last.Outcome == "policy" || last.Facts["allowed_ips_match"] == "false" || last.Facts["keepalive_match"] == "false" {
		return true
	}
	if check.Kind != "mihomo" {
		return false
	}
	mode := last.Facts["mode"]
	if check.Adapter.ExpectedMode != "" && mode != "" && mode != "other" && mode != check.Adapter.ExpectedMode {
		return true
	}
	if want := check.Adapter.ExpectedTUN; want != nil {
		actual := last.Facts["tun_enabled"]
		return actual == "true" && !*want || actual == "false" && *want
	}
	return false
}
