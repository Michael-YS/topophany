// Package vercel interprets Vercel-specific reported metadata.
package vercel

import (
	"net/http"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/edgeheaders"
)

// HeaderFacts reads response labels without establishing function health.
func HeaderFacts(h http.Header) (map[string]string, bool) {
	id := h.Get("X-Vercel-Id")
	if id == "" {
		return nil, false
	}
	out := map[string]string{"edge_provider": "vercel"}
	// The first region is the receiving edge region. Additional regions
	// describe reported routing; their position alone is not function proof.
	// Never sanitize malformed regions into plausible concatenated labels.
	if regions := routeRegions(id); len(regions) > 0 {
		out["vercel_edge_region"] = regions[0]
		out["vercel_route_regions"] = strings.Join(regions, ":")
	}
	edgeheaders.SetFact(out, "edge_cache", strings.ToUpper(h.Get("X-Vercel-Cache")))
	return out, true
}

func routeRegions(id string) []string {
	if len(id) > 1024 {
		return nil
	}
	prefix, request, ok := strings.Cut(id, "::")
	if !ok || request == "" || strings.Contains(request, "::") || strings.Count(prefix, ":") >= 8 {
		return nil
	}
	regions := strings.Split(prefix, ":")
	for _, r := range regions {
		// Bounded region codes such as hnd1 and iad1; retain exact spelling.
		if len(r) < 4 || len(r) > 5 {
			return nil
		}
		for i, c := range []byte(r) {
			if i < 3 && (c < 'a' || c > 'z') || i >= 3 && (c < '0' || c > '9') {
				return nil
			}
		}
	}
	return regions
}
