package view

import (
	"strings"
	"testing"
)

func TestVercelLocationIsReportedAsRegions(t *testing.T) {
	s := edgeAnswer(map[string]string{"edge_provider": "vercel", "vercel_edge_region": "hnd1", "vercel_route_regions": "hnd1:iad1:sfo1", "edge_cache": "MISS"})
	for _, want := range []string{"edge region hnd1", "reported regions hnd1 → iad1 → sfo1", "cache MISS"} {
		if !strings.Contains(s, want) {
			t.Fatal("missing region evidence", s)
		}
	}
	if strings.Contains(s, "PoP") || strings.Contains(s, "function") {
		t.Fatal("routing position claimed unmeasured location", s)
	}
	legacy := edgeAnswer(map[string]string{"edge_provider": "vercel", "edge_pop": "hnd1", "edge_region": "iad1"})
	if !strings.Contains(legacy, "role unverified") || strings.Contains(legacy, "edge region iad1") || strings.Contains(legacy, "function") {
		t.Fatal("historical metadata was reinterpreted", legacy)
	}
}
