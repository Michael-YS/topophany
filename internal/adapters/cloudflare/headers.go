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
	// Cloudflare sets "challenge" on challenge pages; other Cloudflare
	// products set other values, such as "guardrails", which this adapter
	// drops. Keep the closed challenge word so it can cross the secret
	// boundary.
	if strings.EqualFold(strings.TrimSpace(h.Get("Cf-Mitigated")), "challenge") {
		edgeheaders.SetFact(out, "edge_mitigation", "challenge")
	}
	return out, true
}
