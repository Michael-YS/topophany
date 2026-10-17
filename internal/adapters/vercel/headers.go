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
	parts := strings.Split(id, "::")
	edgeheaders.SetFact(out, "edge_pop", parts[0])
	if len(parts) > 2 {
		edgeheaders.SetFact(out, "edge_region", parts[len(parts)-2])
	}
	edgeheaders.SetFact(out, "edge_cache", strings.ToUpper(h.Get("X-Vercel-Cache")))
	return out, true
}
