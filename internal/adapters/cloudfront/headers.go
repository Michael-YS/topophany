// Package cloudfront interprets CloudFront-specific reported metadata.
package cloudfront

import (
	"net/http"

	"github.com/WhitenWhiten/topophany/internal/adapters/edgeheaders"
)

// HeaderFacts reads response labels without establishing origin reachability.
func HeaderFacts(h http.Header) (map[string]string, bool) {
	pop := h.Get("X-Amz-Cf-Pop")
	if pop == "" {
		return nil, false
	}
	out := map[string]string{"edge_provider": "cloudfront"}
	edgeheaders.SetFact(out, "edge_pop", pop)
	edgeheaders.SetFact(out, "edge_cache", edgeheaders.CacheWord(h.Get("X-Cache")))
	return out, true
}
