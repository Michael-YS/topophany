package view

import (
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Edges and platforms: nodes that are not machines you run. An edge (kind
// "edge") is an anycast network that answers for the origins behind it and
// can run code itself, such as Cloudflare or Vercel; a platform (kind
// "platform") hosts managed services, such as Supabase. Endpoints name the
// edges and proxies in front of them in via.

// providers are the providers named in evidence by their own spelling; any
// other provider is shown as written.
var providers = map[string]string{
	"cloudflare": "Cloudflare",
	"vercel":     "Vercel",
	"supabase":   "Supabase",
	"netlify":    "Netlify",
	"fastly":     "Fastly",
	"cloudfront": "CloudFront",
	"aws":        "AWS",
	"fly":        "Fly.io",
	"render":     "Render",
	"railway":    "Railway",
	"deno":       "Deno Deploy",
}

// ProviderName is how a provider is named, e.g. "Cloudflare".
func ProviderName(p string) string {
	if name, ok := providers[strings.ToLower(p)]; ok {
		return name
	}
	return p
}

// KindLabel is a node's kind with its provider, e.g. "edge · Cloudflare".
func KindLabel(n model.Node) string {
	if n.Provider == "" {
		return n.Kind
	}
	if n.Kind == "" {
		return ProviderName(n.Provider)
	}
	return n.Kind + " · " + ProviderName(n.Provider)
}

// ViaNodes lists the edges and proxies in front of the given endpoints, in
// order and once each.
func ViaNodes(t model.Config, endpoints []string) []string {
	out := []string{}
	for _, ep := range t.Endpoints {
		if !slices.Contains(endpoints, ep.ID) {
			continue
		}
		for _, v := range ep.Via {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

// viaNames is ViaNodes by name.
func viaNames(t model.Config, endpoints []string, names map[string]string) []string {
	out := ViaNodes(t, endpoints)
	for i, id := range out {
		out[i] = names[id]
	}
	return out
}

// edgeAnswer summarizes the edge evidence of an HTTP observation, e.g.
// "Cloudflare NRT · cache DYNAMIC"; "" when no edge said it answered.
func edgeAnswer(facts map[string]string) string {
	p := facts["edge_provider"]
	if p == "" {
		return ""
	}
	s := ProviderName(p)
	if pop := facts["edge_pop"]; pop != "" {
		s += " " + pop
	}
	if r := facts["edge_region"]; r != "" {
		s += " → " + r
	}
	if c := facts["edge_cache"]; c != "" {
		s += " · cache " + c
	}
	return s
}
