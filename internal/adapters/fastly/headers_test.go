package fastly_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/fastly"
)

func TestHeaderFacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    map[string]string
	}{
		{"unrecognized", http.Header{"X-Served-By": {"CACHE-nrt-NRT"}}, nil},
		{"nearest hop", http.Header{"X-Served-By": {"cache-iad-IAD, cache-nrt-NRT"}, "X-Cache": {"MISS, HIT"}}, map[string]string{"edge_provider": "fastly", "edge_pop": "NRT", "edge_cache": "HIT"}},
		{"empty last hop", http.Header{"X-Served-By": {"cache-iad-IAD, "}, "X-Cache": {"HIT"}}, map[string]string{"edge_provider": "fastly", "edge_cache": "HIT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, matched := fastly.HeaderFacts(tc.headers)
			if matched != (tc.want != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, matched %t; want %#v", got, matched, tc.want)
			}
		})
	}
}
