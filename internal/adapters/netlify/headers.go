// Package netlify interprets Netlify-specific reported metadata.
package netlify

import (
	"net/http"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/edgeheaders"
)

// HeaderFacts reads response labels without establishing function health.
func HeaderFacts(h http.Header) (map[string]string, bool) {
	if h.Get("X-Nf-Request-Id") == "" && !strings.EqualFold(h.Get("Server"), "Netlify") {
		return nil, false
	}
	out := map[string]string{"edge_provider": "netlify"}
	for _, part := range strings.Split(edgeheaders.LastHop(h.Get("Cache-Status")), ";")[1:] {
		switch part = strings.TrimSpace(part); {
		case part == "hit":
			out["edge_cache"] = "HIT"
		case strings.HasPrefix(part, "fwd="):
			out["edge_cache"] = "MISS"
		}
	}
	return out, true
}
