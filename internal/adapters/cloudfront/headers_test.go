package cloudfront_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudfront"
)

func TestHeaderFacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    map[string]string
	}{
		{"unrecognized", http.Header{"X-Cache": {"Hit from cloudfront"}}, nil},
		{"normal", http.Header{"X-Amz-Cf-Pop": {"NRT57-P1"}, "X-Cache": {"Hit from cloudfront"}}, map[string]string{"edge_provider": "cloudfront", "edge_pop": "NRT57-P1", "edge_cache": "HIT"}},
		{"last cache hop", http.Header{"X-Amz-Cf-Pop": {"IAD"}, "X-Cache": {"MISS, HIT"}}, map[string]string{"edge_provider": "cloudfront", "edge_pop": "IAD", "edge_cache": "HIT"}},
		{"closed cache labels", http.Header{"X-Amz-Cf-Pop": {"IAD"}, "X-Cache": {"private-token"}}, map[string]string{"edge_provider": "cloudfront", "edge_pop": "IAD"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, matched := cloudfront.HeaderFacts(tc.headers)
			if matched != (tc.want != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, matched %t; want %#v", got, matched, tc.want)
			}
		})
	}
}
