package view

import (
	"slices"
	"sort"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Edges and platforms: nodes that are not machines you run. An edge (kind
// "edge") is an anycast network that answers for the origins behind it and
// can run code itself, such as Cloudflare or Vercel; a platform (kind
// "platform") hosts managed services, such as Supabase. Endpoints name the
// edges and proxies in front of them in via.

// Provider is how a provider is shown: its own spelling of its name, its
// brand color as 0xRRGGBB (0 for none), its logo where the terminal's font
// is a Nerd Font (0 for none) and a mark of a few letters that stands in
// for its name where that has no room ("" for none).
type Provider struct {
	Name  string
	Color uint32
	Glyph rune
	Mark  string
}

// providers are the providers the views know; any other provider is shown
// as written. The logos are the Nerd Fonts' (nerdfonts.com, v3.3 or later).
var providers = map[string]Provider{
	"cloudflare":   {"Cloudflare", 0xF6821F, '\ue792', "CF"},
	"vercel":       {"Vercel", 0xE5E7EB, '\ue8d3', "▲"},
	"supabase":     {"Supabase", 0x3ECF8E, '\ue8b6', "SB"},
	"gcp":          {"GCP", 0x4285F4, '\ue7f1', "GCP"},
	"azure":        {"Azure", 0x0089D6, '\ue754', "AZ"},
	"aws":          {"AWS", 0xFF9900, '\ue7ad', "AWS"},
	"oracle":       {"Oracle", 0xC74634, '\U000F0EA1', "OCI"}, // a ring: the fonts' Oracle logo is a wordmark too thin to read
	"deno":         {"Deno Deploy", 0x70FFAF, '\ue7c0', "DN"},
	"linode":       {"Linode", 0x00A95C, '\uf2b8', "LN"},
	"digitalocean": {"DigitalOcean", 0x0080FF, '\ue7ae', "DO"},
	"netlify":      {"Netlify", 0x32E6E2, 0, ""},
	"fastly":       {"Fastly", 0xFF5A5F, 0, ""},
	"cloudfront":   {"CloudFront", 0xFF9900, 0, ""},
	"fly":          {"Fly.io", 0xA78BFA, 0, ""},
	"render":       {"Render", 0x46E3B7, 0, ""},
	"railway":      {"Railway", 0xC4B5FD, 0, ""},
}

// providerAliases are other names a topology may give a known provider.
var providerAliases = map[string]string{
	"google": "gcp", "googlecloud": "gcp", "google-cloud": "gcp", "gce": "gcp",
	"microsoft": "azure", "amazon": "aws", "oci": "oracle", "oraclecloud": "oracle",
	"deno-deploy": "deno", "do": "digitalocean",
}

// LookupProvider finds a provider the views know, by any of its names.
func LookupProvider(p string) (Provider, bool) {
	k := strings.ToLower(strings.TrimSpace(p))
	if a, ok := providerAliases[k]; ok {
		k = a
	}
	v, ok := providers[k]
	return v, ok
}

// ProviderName is how a provider is named, e.g. "Cloudflare".
func ProviderName(p string) string {
	if v, ok := LookupProvider(p); ok {
		return v.Name
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
// "Cloudflare NRT · cache DYNAMIC · challenge"; "" when no edge said it
// answered.
func edgeAnswer(facts map[string]string) string {
	p := facts["edge_provider"]
	if p == "" {
		return ""
	}
	s := ProviderName(p)
	if p == "vercel" {
		if r := facts["vercel_edge_region"]; r != "" {
			s += " · edge region " + r
		} else if facts["edge_pop"] != "" || facts["edge_region"] != "" {
			s += " · legacy region labels (role unverified)"
		}
		if route := facts["vercel_route_regions"]; strings.Contains(route, ":") {
			s += " · reported regions " + strings.ReplaceAll(route, ":", " → ")
		}
		if c := facts["edge_cache"]; c != "" {
			s += " · cache " + c
		}
	} else {
		if pop := facts["edge_pop"]; pop != "" {
			s += " " + pop
		}
		if r := facts["edge_region"]; r != "" {
			s += " → " + r
		}
		if c := facts["edge_cache"]; c != "" {
			s += " · cache " + c
		}
	}
	if facts["edge_mitigation"] == "challenge" {
		s += " · challenge"
	}
	return s
}

// indexServiceVia resolves each service's endpoint references once per fleet.
// Preserve ViaNodes' topology order even when the service lists endpoints in a
// different order or repeats a reference.
func indexServiceVia(t model.Config) map[string][]string {
	positions := make(map[string]int, len(t.Endpoints))
	for i, ep := range t.Endpoints {
		positions[ep.ID] = i
	}
	out := make(map[string][]string, len(t.Services))
	for _, service := range t.Services {
		indices := make([]int, 0, len(service.Endpoints))
		for _, id := range service.Endpoints {
			if i, ok := positions[id]; ok {
				indices = append(indices, i)
			}
		}
		sort.Ints(indices)
		seen := map[string]bool{}
		for _, i := range slices.Compact(indices) {
			for _, id := range t.Endpoints[i].Via {
				if !seen[id] {
					out[service.ID] = append(out[service.ID], id)
					seen[id] = true
				}
			}
		}
	}
	return out
}
