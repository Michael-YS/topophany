package view

import "testing"

// The edge's evidence line names a challenge page after the cache status it
// does not explain, and only the closed word the adapter kept.
func TestEdgeAnswerNamesAChallenge(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts map[string]string
		want  string
	}{
		{"challenge", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": "DYNAMIC", "edge_mitigation": "challenge"}, "Cloudflare NRT · cache DYNAMIC · challenge"},
		{"challenge without cache", map[string]string{"edge_provider": "cloudflare", "edge_mitigation": "challenge"}, "Cloudflare · challenge"},
		{"other mitigation", map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_mitigation": "guardrails"}, "Cloudflare NRT"},
		{"vercel unchanged", map[string]string{"edge_provider": "vercel", "vercel_edge_region": "hnd1", "edge_cache": "MISS"}, "Vercel · edge region hnd1 · cache MISS"},
		{"no provider", map[string]string{"edge_mitigation": "challenge"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := edgeAnswer(tc.facts); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
