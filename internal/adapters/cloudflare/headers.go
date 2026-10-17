// Package cloudflare interprets Cloudflare-specific reported metadata.
package cloudflare

import (
	"net/http"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/edgeheaders"
)

// HeaderFacts reads response labels without establishing origin reachability.
func HeaderFacts(h http.Header) (map[string]string, bool) {
	ray := h.Get("Cf-Ray")
	if ray == "" {
		return nil, false
	}
	out := map[string]string{"edge_provider": "cloudflare"}
	if strings.Contains(ray, "-") {
		edgeheaders.SetFact(out, "edge_pop", ray[strings.LastIndexByte(ray, '-')+1:])
	}
	edgeheaders.SetFact(out, "edge_cache", strings.ToUpper(h.Get("Cf-Cache-Status")))
	return out, true
}
