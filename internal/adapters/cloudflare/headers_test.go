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
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, matched := cloudflare.HeaderFacts(tc.headers)
			if matched != (tc.want != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, matched %t; want %#v", got, matched, tc.want)
			}
		})
	}
}
