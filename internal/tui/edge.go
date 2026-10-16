package tui

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Edges, platforms and the nodes a known provider runs (see the view
// package) are drawn in the provider's color, and where the terminal's
// font is a Nerd Font they wear its logo before their names: on their
// cards, on the maps and wherever an edge is named in front of something.
// Where an edge's name has no room, its logo or its mark stands in.

// providerColor is a known provider's brand color, otherwise c.
func providerColor(p string, c Color) Color {
	if v, ok := view.LookupProvider(p); ok && v.Color != 0 {
		return Hex(v.Color)
	}
	return c
}

// nodeHue is an edge's or a platform's brand color, else c: a machine you
// run keeps the color of its group, whoever runs it.
func nodeHue(n model.Node, c Color) Color {
	if hosted(n.Kind) {
		return providerColor(n.Provider, c)
	}
	return c
}

// hosted reports whether a node kind is something you deploy to rather
// than a machine you run.
func hosted(kind string) bool { return kind == "edge" || kind == "platform" }

// glyph is a provider's logo where the terminal can draw it, else 0.
func (m *Model) glyph(provider string) rune {
	if v, ok := view.LookupProvider(provider); ok && m.NerdFont {
		return v.Glyph
	}
	return 0
}

// logo leads a name with its provider's logo and a space, in the brand's
// color; nothing where the terminal cannot draw it.
func (m *Model) logo(provider string) []span {
	g := m.glyph(provider)
	if g == 0 {
		return nil
	}
	return []span{{string(g), fg(Mix(providerColor(provider, cText), cText, .12))}, {" ", Style{}}}
}

// logoText is a name as the maps and tables write it, led by its
// provider's logo where the terminal can draw it.
func (m *Model) logoText(provider, name string) string {
	if g := m.glyph(provider); g != 0 {
		return string(g) + " " + name
	}
	return name
}

// standIn is what stands for a node named where its name has no room: an
// edge's or a platform's logo, else its provider's mark; nil for anything
// else, whose name is cut short instead.
func (m *Model) standIn(n model.Node) []span {
	v, ok := view.LookupProvider(n.Provider)
	if !ok || !hosted(n.Kind) {
		return nil
	}
	if g := m.glyph(n.Provider); g != 0 {
		return []span{{string(g), fg(Mix(providerColor(n.Provider, cText), cText, .12))}}
	}
	if v.Mark == "" {
		return nil
	}
	return []span{{v.Mark, fg(providerColor(n.Provider, cMuted)).bold()}}
}

// logoRows leads the nodes a service's HOST column names, the edges in
// front and the node it runs on, with their logos; the rows are copies.
func (m *Model) logoRows(rows []Row) []Row {
	if !m.NerdFont || m.Tab != tabServices {
		return rows
	}
	providers := map[string]string{}
	for _, n := range m.Snapshot.Topology.Nodes {
		name := n.Name
		if name == "" {
			name = n.ID
		}
		if m.glyph(n.Provider) != 0 {
			providers[name] = n.Provider
		}
	}
	if len(providers) == 0 {
		return rows
	}
	out := make([]Row, len(rows))
	for i, r := range rows {
		out[i] = r
		if r.Kind != "service" || len(r.Cols) == 0 {
			continue
		}
		hops := strings.Split(r.Cols[0], " ⇢ ")
		for k, hop := range hops {
			name, rest, _ := strings.Cut(hop, " · ")
			if p, ok := providers[name]; ok {
				hops[k] = m.logoText(p, name)
				if rest != "" {
					hops[k] += " · " + rest
				}
			}
		}
		out[i].Cols = append([]string{strings.Join(hops, " ⇢ ")}, r.Cols[1:]...)
	}
	return out
}
