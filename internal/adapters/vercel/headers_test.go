package vercel_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/vercel"
)

func TestHeaderFacts(t *testing.T) {
	for _, tc := range []struct{ name, id, region, route string }{
		{"single region", "sin1::request", "sin1", "sin1"},
		{"two regions", "hnd1:iad1::request", "hnd1", "hnd1:iad1"},
		{"multiple regions", "hnd1:iad1:sfo1::request", "hnd1", "hnd1:iad1:sfo1"},
		{"eight regions", strings.Repeat("iad1:", 7) + "sfo1::request", "iad1", strings.Repeat("iad1:", 7) + "sfo1"},
		{"excess regions", strings.Repeat("iad1:", 8) + "sfo1::request", "", ""},
		{"old malformed format", "hnd1::iad1::request", "", ""},
		{"missing region", "::request", "", ""},
		{"missing request", "iad1::", "", ""},
		{"missing separator", "iad1", "", ""},
		{"blank hop", "iad1::sfo1::request", "", ""},
		{"empty intermediate hop", "iad1::request::id", "", ""},
		{"invalid characters", "iad1/sfo1::request", "", ""},
		{"unicode", "東京1::request", "", ""},
		{"control", "iad1\x1b[2J::request", "", ""},
		{"oversize", "iad1::" + strings.Repeat("x", 1024), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{"X-Vercel-Id": {tc.id}, "X-Vercel-Cache": {"miss"}}
			before := h.Clone()
			got, matched := vercel.HeaderFacts(h)
			want := map[string]string{"edge_provider": "vercel", "edge_cache": "MISS"}
			if tc.region != "" {
				want["vercel_edge_region"] = tc.region
				want["vercel_route_regions"] = tc.route
			}
			if !matched || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(before, h) {
				t.Fatalf("got %#v, matched %v; want %#v", got, matched, want)
			}
			got["edge_provider"] = "mutated"
			again, _ := vercel.HeaderFacts(h)
			if again["edge_provider"] != "vercel" {
				t.Fatal("shared result map")
			}
		})
	}
	for _, h := range []http.Header{nil, {}, {"Server": {"Vercel"}}} {
		if got, matched := vercel.HeaderFacts(h); matched || got != nil {
			t.Fatal("recognized absent ID", got)
		}
	}
}
