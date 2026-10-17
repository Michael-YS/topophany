package vercel_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/vercel"
)

func TestHeaderFacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    map[string]string
	}{
		{"unrecognized", http.Header{"Server": {"Vercel"}}, nil},
		{"edge and function", http.Header{"X-Vercel-Id": {"hnd1::iad1::request"}, "X-Vercel-Cache": {"miss"}}, map[string]string{"edge_provider": "vercel", "edge_pop": "hnd1", "edge_region": "iad1", "edge_cache": "MISS"}},
		{"edge only", http.Header{"X-Vercel-Id": {"sin1::request"}}, map[string]string{"edge_provider": "vercel", "edge_pop": "sin1"}},
		{"penultimate region", http.Header{"X-Vercel-Id": {"a::b::c::request"}}, map[string]string{"edge_provider": "vercel", "edge_pop": "a", "edge_region": "c"}},
		{"recognized empty labels", http.Header{"X-Vercel-Id": {"::"}}, map[string]string{"edge_provider": "vercel"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, matched := vercel.HeaderFacts(tc.headers)
			if matched != (tc.want != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, matched %t; want %#v", got, matched, tc.want)
			}
		})
	}
}
