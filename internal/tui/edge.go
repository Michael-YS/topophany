package tui

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Edges and platforms: nodes that are not machines you run. An edge (kind
// "edge") is an anycast network that answers for the origins behind it and
// can run code itself, such as Cloudflare or Vercel; a platform (kind
// "platform") hosts managed services, such as Supabase. Endpoints name the
// edges and proxies in front of them in via.

type brand struct {
	name string
	c    Color
}

// brands are the providers the UI names and colors; any other provider is
// shown as written.
var brands = map[string]brand{
	"cloudflare": {"Cloudflare", Hex(0xF6821F)},
	"vercel":     {"Vercel", Hex(0xE5E7EB)},
	"supabase":   {"Supabase", Hex(0x3ECF8E)},
	"netlify":    {"Netlify", Hex(0x32E6E2)},
	"fastly":     {"Fastly", Hex(0xFF5A5F)},
	"cloudfront": {"CloudFront", Hex(0xFF9900)},
	"aws":        {"AWS", Hex(0xFF9900)},
	"fly":        {"Fly.io", Hex(0xA78BFA)},
	"render":     {"Render", Hex(0x46E3B7)},
	"railway":    {"Railway", Hex(0xC4B5FD)},
	"deno":       {"Deno Deploy", Hex(0x70FFAF)},
}

func providerName(p string) string {
	if b, ok := brands[strings.ToLower(p)]; ok {
		return b.name
	}
	return p
}

// providerColor is a known provider's brand color, otherwise c.
func providerColor(p string, c Color) Color {
	if b, ok := brands[strings.ToLower(p)]; ok {
		return b.c
	}
	return c
}

// hosted reports whether a node kind is something you deploy to rather
// than a machine you run.
func hosted(kind string) bool { return kind == "edge" || kind == "platform" }

// kindLabel is a node's kind with its provider, e.g. "edge · Cloudflare".
func kindLabel(n model.Node) string {
	if n.Provider == "" {
		return n.Kind
	}
	if n.Kind == "" {
		return providerName(n.Provider)
	}
	return n.Kind + " · " + providerName(n.Provider)
}

// viaNodes lists the edges and proxies in front of the given endpoints, in
// order and once each.
func viaNodes(t model.Config, endpoints []string) []string {
	out := []string{}
	for _, ep := range t.Endpoints {
		if !contains(endpoints, ep.ID) {
			continue
		}
		for _, v := range ep.Via {
			if !contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

// viaNames is viaNodes by name.
func viaNames(t model.Config, endpoints []string, names map[string]string) []string {
	out := viaNodes(t, endpoints)
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
	s := providerName(p)
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
