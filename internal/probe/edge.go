package probe

import (
	"net/http"
	"strings"
)

// EdgeFacts reads which edge network answered an HTTP request from the
// headers it adds to every response: its name, the point of presence that
// served the request, the function region behind it where one is named,
// and whether its cache answered without asking the origin. They are what
// the response says, not verified routing: an origin can send them too.
func EdgeFacts(h http.Header) map[string]string {
	out := map[string]string{}
	set := func(k, v string) {
		if v = token(v); v != "" {
			out[k] = v
		}
	}
	// Lists name the hop nearest the client last.
	last := func(list string) string {
		parts := strings.Split(list, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	// "Hit from cloudfront" → HIT, "MISS, HIT" → HIT
	word := func(v string) string {
		if f := strings.Fields(last(v)); len(f) > 0 {
			return strings.ToUpper(f[0])
		}
		return ""
	}
	switch {
	case h.Get("Cf-Ray") != "":
		out["edge_provider"] = "cloudflare"
		// "8f2c1b2a3d4e5f60-NRT"
		if ray := h.Get("Cf-Ray"); strings.Contains(ray, "-") {
			set("edge_pop", ray[strings.LastIndexByte(ray, '-')+1:])
		}
		set("edge_cache", strings.ToUpper(h.Get("Cf-Cache-Status")))
	case h.Get("X-Vercel-Id") != "":
		out["edge_provider"] = "vercel"
		// "hnd1::iad1::abcd-…": the edge region, then a function region.
		parts := strings.Split(h.Get("X-Vercel-Id"), "::")
		set("edge_pop", parts[0])
		if len(parts) > 2 {
			set("edge_region", parts[len(parts)-2])
		}
		set("edge_cache", strings.ToUpper(h.Get("X-Vercel-Cache")))
	case h.Get("X-Amz-Cf-Pop") != "":
		out["edge_provider"] = "cloudfront"
		set("edge_pop", h.Get("X-Amz-Cf-Pop"))
		set("edge_cache", word(h.Get("X-Cache")))
	case strings.HasPrefix(h.Get("X-Served-By"), "cache-"):
		out["edge_provider"] = "fastly"
		// "cache-iad-kiad7000025-IAD, cache-nrt-rjtf7700040-NRT"
		if hop := last(h.Get("X-Served-By")); strings.Contains(hop, "-") {
			set("edge_pop", hop[strings.LastIndexByte(hop, '-')+1:])
		}
		set("edge_cache", word(h.Get("X-Cache")))
	case h.Get("X-Nf-Request-Id") != "" || strings.EqualFold(h.Get("Server"), "Netlify"):
		out["edge_provider"] = "netlify"
		// RFC 9211: `"Netlify Edge"; hit` or `"Netlify Edge"; fwd=miss`.
		for _, p := range strings.Split(last(h.Get("Cache-Status")), ";")[1:] {
			switch p = strings.TrimSpace(p); {
			case p == "hit":
				out["edge_cache"] = "HIT"
			case strings.HasPrefix(p, "fwd="):
				out["edge_cache"] = "MISS"
			}
		}
	}
	return out
}

// token keeps a short run of the characters such a label is made of.
func token(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if b.Len() >= 24 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
