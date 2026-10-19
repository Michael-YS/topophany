package view

import "testing"

// The edge's evidence line names a challenge page, an Access login and
// Cloudflare's own RTT reading after the cache status it does not explain,
// in that order and only from the closed words and bounded numbers the
// adapter kept.
func TestEdgeAnswerNamesEdgeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts map[string]string
		want  string
	}{
		{"challenge", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": "DYNAMIC", "edge_mitigation": "challenge"}, "Cloudflare NRT · cache DYNAMIC · challenge"},
		{"challenge without cache", map[string]string{"edge_provider": "cloudflare", "edge_mitigation": "challenge"}, "Cloudflare · challenge"},
		{"other mitigation", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_mitigation": "guardrails"}, "Cloudflare NRT"},
		{"access login", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": "DYNAMIC", "edge_access": "login_redirect"}, "Cloudflare NRT · cache DYNAMIC · Access login"},
		{"access without cache", map[string]string{"edge_provider": "cloudflare", "edge_access": "login_redirect"}, "Cloudflare · Access login"},
		{"access and challenge", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": "DYNAMIC", "edge_mitigation": "challenge", "edge_access": "login_redirect"}, "Cloudflare NRT · cache DYNAMIC · challenge · Access login"},
		{"other access", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_access": "policy"}, "Cloudflare NRT"},
		{"edge rtt", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": "DYNAMIC", "edge_rtt_ms": "38"}, "Cloudflare NRT · cache DYNAMIC · edge rtt 38 ms"},
		{"edge rtt without other labels", map[string]string{"edge_provider": "cloudflare", "edge_rtt_ms": "2"}, "Cloudflare · edge rtt 2 ms"},
		{"edge rtt below a millisecond", map[string]string{"edge_provider": "cloudflare", "edge_rtt_ms": "0"}, "Cloudflare · edge rtt <1 ms"},
		{"edge rtt malformed", map[string]string{"edge_provider": "cloudflare", "edge_rtt_ms": "abc"}, "Cloudflare"},
		{"edge rtt too long", map[string]string{"edge_provider": "cloudflare", "edge_rtt_ms": "1234567890123"}, "Cloudflare"},
		{"edge rtt last", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": "HIT", "edge_mitigation": "challenge", "edge_access": "login_redirect", "edge_rtt_ms": "1"}, "Cloudflare NRT · cache HIT · challenge · Access login · edge rtt 1 ms"},
		{"vercel unchanged", map[string]string{"edge_provider": "vercel", "vercel_edge_region": "hnd1", "edge_cache": "MISS"}, "Vercel · edge region hnd1 · cache MISS"},
		{"vercel ignores a stray rtt", map[string]string{"edge_provider": "vercel", "vercel_edge_region": "hnd1", "edge_rtt_ms": "38"}, "Vercel · edge region hnd1"},
		{"no provider", map[string]string{"edge_mitigation": "challenge"}, ""},
		{"no provider with access", map[string]string{"edge_access": "login_redirect"}, ""},
		{"no provider with rtt", map[string]string{"edge_rtt_ms": "38"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := edgeAnswer(tc.facts); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
