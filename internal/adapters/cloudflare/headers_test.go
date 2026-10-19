package cloudflare_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
)

func TestHeaderFacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    map[string]string
	}{
		{"unrecognized", http.Header{}, nil},
		{"cache alone", http.Header{"Cf-Cache-Status": {"HIT"}}, nil},
		{"normal", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Cache-Status": {"dynamic"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_cache": "DYNAMIC"}},
		{"recognized without usable labels", http.Header{"Cf-Ray": {"ray-东京"}, "Cf-Cache-Status": {"private-token"}}, map[string]string{"edge_provider": "cloudflare"}},
		{"mitigation challenge", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"challenge"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_mitigation": "challenge"}},
		{"mitigation without ray", http.Header{"Cf-Mitigated": {"challenge"}}, nil},
		{"mitigation empty", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {""}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"}},
		{"mitigation other value", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"managed_challenge"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"}},
		{"mitigation guardrails", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"guardrails"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"}},
		{"mitigation mixed case", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"  ChAlLeNgE\t"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_mitigation": "challenge"}},
		{"mitigation first value wins", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"guardrails", "challenge"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"}},
		{"mitigation first challenge wins", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"challenge", "guardrails"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "edge_mitigation": "challenge"}},
		{"mitigation token-looking", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"challengeSECRET"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"}},
		{"mitigation control characters", http.Header{"Cf-Ray": {"ray-NRT"}, "Cf-Mitigated": {"challenge\x1b[2J"}}, map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, matched := cloudflare.HeaderFacts(tc.headers)
			if matched != (tc.want != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, matched %t; want %#v", got, matched, tc.want)
			}
		})
	}
}
