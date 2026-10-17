// Package fastly interprets Fastly-specific reported metadata.
package fastly

import (
	"net/http"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/edgeheaders"
)

// HeaderFacts reads response labels without establishing origin reachability.
func HeaderFacts(h http.Header) (map[string]string, bool) {
	served := h.Get("X-Served-By")
	if !strings.HasPrefix(served, "cache-") {
		return nil, false
	}
	out := map[string]string{"edge_provider": "fastly"}
	if hop := edgeheaders.LastHop(served); strings.Contains(hop, "-") {
		edgeheaders.SetFact(out, "edge_pop", hop[strings.LastIndexByte(hop, '-')+1:])
	}
	edgeheaders.SetFact(out, "edge_cache", edgeheaders.CacheWord(h.Get("X-Cache")))
	return out, true
}
