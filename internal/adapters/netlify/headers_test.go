package netlify_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/netlify"
)

func TestHeaderFacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    map[string]string
	}{
		{"unrecognized", http.Header{"Server": {" Netlify "}}, nil},
		{"server case", http.Header{"Server": {"NETLIFY"}, "Cache-Status": {"\"Netlify Edge\"; hit"}}, map[string]string{"edge_provider": "netlify", "edge_cache": "HIT"}},
		{"request ID", http.Header{"X-Nf-Request-Id": {"request"}}, map[string]string{"edge_provider": "netlify"}},
		{"last hop", http.Header{"Server": {"Netlify"}, "Cache-Status": {"\"Netlify Durable\"; fwd=bypass, \"Netlify Edge\"; hit"}}, map[string]string{"edge_provider": "netlify", "edge_cache": "HIT"}},
		{"last directive wins", http.Header{"Server": {"Netlify"}, "Cache-Status": {"\"Netlify Edge\"; hit; fwd=miss"}}, map[string]string{"edge_provider": "netlify", "edge_cache": "MISS"}},
		{"reverse directives", http.Header{"Server": {"Netlify"}, "Cache-Status": {"\"Netlify Edge\"; fwd=miss; hit"}}, map[string]string{"edge_provider": "netlify", "edge_cache": "HIT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, matched := netlify.HeaderFacts(tc.headers)
			if matched != (tc.want != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, matched %t; want %#v", got, matched, tc.want)
			}
		})
	}
}
