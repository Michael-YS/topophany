package tui

import "strings"

// Edges and platforms (see the view package) are drawn in their provider's
// color where it is known.

// brands are the colors of the providers the view names; any other
// provider takes the color of its context.
var brands = map[string]Color{
	"cloudflare": Hex(0xF6821F),
	"vercel":     Hex(0xE5E7EB),
	"supabase":   Hex(0x3ECF8E),
	"netlify":    Hex(0x32E6E2),
	"fastly":     Hex(0xFF5A5F),
	"cloudfront": Hex(0xFF9900),
	"aws":        Hex(0xFF9900),
	"fly":        Hex(0xA78BFA),
	"render":     Hex(0x46E3B7),
	"railway":    Hex(0xC4B5FD),
	"deno":       Hex(0x70FFAF),
}

// providerColor is a known provider's brand color, otherwise c.
func providerColor(p string, c Color) Color {
	if b, ok := brands[strings.ToLower(p)]; ok {
		return b
	}
	return c
}

// hosted reports whether a node kind is something you deploy to rather
// than a machine you run.
func hosted(kind string) bool { return kind == "edge" || kind == "platform" }
