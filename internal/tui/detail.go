package tui

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/view"
)

// The evidence panel: a title block (name, state pill, kind and identity)
// over a body that lays "Key: value" detail lines out as an aligned table.
// Values with meaning (health, freshness, outcome) are colored; caveats that
// limit what the evidence proves are kept, in a quieter voice.

type span struct {
	s  string
	st Style
}
type vline []span

// statePill is the bracketed state shown beside an entity's name.
func statePill(r Row) (string, Style) {
	word, c := view.Pill(r), healthColor(r.Health)
	glyph := string(healthGlyph(r.Health))
	switch word {
	case "OPEN":
		glyph, c = "⚑", cDown
	case "CACHED":
		glyph, c = "◌", cWarn
	case "AFFECTED", "OUTDATED":
		c = cWarn
	}
	return " " + glyph + " " + word + " ", fg(c).Bg(Mix(c, cInk, .82)).bold()
}

// detailKV splits "Key: value" lines whose key reads like a label; "Key: "
// with nothing after it is a key with an empty value, not a heading.
func detailKV(l string) (string, string, bool) {
	k := strings.Index(l+" ", ": ")
	if k <= 0 || k+1 == len(l) && !strings.HasSuffix(l, ": ") {
		return "", "", false
	}
	key := l[:k]
	if width(key) > 30 || strings.Count(key, " ") > 4 || strings.Contains(key, "→") {
		return "", "", false
	}
	return key, strings.TrimSpace(l[min(len(l), k+2):]), true
}

func detailHeading(l string) bool {
	return strings.HasSuffix(l, ":") && !strings.Contains(strings.TrimSuffix(l, ":"), ": ")
}

var caveats = []string{"No inference", "A declared path", "Directed initiation", "Health aggregates", "Each ", "An edge in front"}

// valueStyle colors values whose words carry state.
func valueStyle(key, v string) (string, Style) {
	switch key {
	case "State", "Health":
		return v, fg(healthColor(v)).bold()
	case "Affected dependency":
		if v == "true" {
			return "yes — on a failing path or dependency", fg(cWarn)
		}
		return "no", fg(cMuted)
	case "Freshness":
		switch v {
		case "fresh":
			return v, fg(Mix(cUp, cText, .35))
		case "stale", "never":
			return v, fg(cWarn)
		}
		return v, fg(cMuted)
	case "Outcome":
		switch v {
		case "ok":
			return v, fg(cUp)
		case "fail", "error":
			return v, fg(cDown).bold()
		case "policy":
			return v, fg(cWarn).bold()
		}
	case "Evidence":
		if v == "measured" {
			return v, fg(cText)
		}
		return v, fg(cMuted)
	}
	if v == "" {
		return "—", fg(cFaint)
	}
	return v, fg(cText)
}

// arrows splits a route so its arrows can carry the accent color.
func arrows(s string, st Style) vline {
	out := vline{}
	for i, part := range strings.Split(s, " → ") {
		if i > 0 {
			out = append(out, span{" → ", fg(cAccent)})
		}
		out = append(out, span{part, st})
	}
	return out
}

func (m *Model) detailBody(r Row, w int) []vline {
	out := []vline{}
	blank := func() {
		if len(out) > 0 && len(out[len(out)-1]) > 0 {
			out = append(out, vline{})
		}
	}
	kw := 0
	for _, l := range r.Details {
		if k, _, ok := detailKV(l); ok && k != "ID" && k != "State" {
			kw = max(kw, width(k))
		}
	}
	kw = min(kw, 22, max(8, w*2/5))
	vw := max(8, w-kw-2)
	for _, raw := range r.Details {
		l := strings.TrimSpace(raw)
		if key, val, ok := detailKV(strings.TrimLeft(raw, " ")); ok && val == "" && l == key+":" {
			if key != "ID" && key != "State" {
				out = append(out, vline{{key + strings.Repeat(" ", max(0, kw-width(key))+2), fg(cMuted)}, {"—", fg(cFaint)}})
			}
			continue
		}
		switch {
		case l == "":
			blank()
		case strings.HasPrefix(l, "CACHED VIEW") || strings.HasPrefix(l, "LOCAL DRAFT"):
			st := fg(cWarn).Bg(Mix(cWarn, cInk, .86))
			if strings.HasPrefix(l, "LOCAL DRAFT") {
				st = fg(cEdit).Bg(Mix(cEdit, cInk, .86))
			}
			for _, wl := range wrap(l, w-2) {
				out = append(out, vline{{" " + wl + strings.Repeat(" ", max(0, w-2-width(wl))) + " ", st}})
			}
		case detailHeading(l):
			blank()
			out = append(out, vline{{strings.ToUpper(strings.TrimSuffix(l, ":")), fg(Mix(cAccent, cMuted, .35)).bold()}})
		default:
			if key, val, ok := detailKV(l); ok {
				if key == "ID" || key == "State" {
					continue
				}
				val, st := valueStyle(key, val)
				if width(key) > kw {
					// A long key takes its own line; the value hangs below it.
					out = append(out, vline{{key, fg(cMuted)}})
				}
				for i, v := range wrap(val, vw) {
					lead := strings.Repeat(" ", kw+2)
					if i == 0 && width(key) <= kw {
						lead = key + strings.Repeat(" ", max(0, kw-width(key))+2)
					}
					out = append(out, append(vline{{lead, fg(cMuted)}}, arrows(v, st)...))
				}
				continue
			}
			st := fg(cText)
			for _, c := range caveats {
				if strings.HasPrefix(l, c) {
					st = fg(cMuted).italic()
				}
			}
			for _, v := range wrap(l, w) {
				out = append(out, arrows(v, st))
			}
		}
	}
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	return out
}

func (m *Model) drawDetail(cv *Canvas, x, y, w, h int, r Row, scroll bool) {
	if h <= 0 || w < 4 {
		return
	}
	if r.Kind == "" {
		cv.Text(x, y, "Nothing selected.", fg(cMuted).italic(), w)
		return
	}
	top := 0
	if h >= 6 {
		label, st := statePill(r)
		pw := width(label)
		cv.Text(x, y, strings.TrimSpace(r.Title), fg(cGlow).bold(), w-pw-1)
		cv.Text(x+w-pw, y, label, st, -1)
		sub := r.Kind + "  ·  " + r.ID
		if _, scope, ok := strings.Cut(r.ID, "/"); ok && r.Kind == "incident" {
			sub = "incident  ·  " + scope
		}
		cv.Text(x, y+1, sub, fg(cFaint), w)
		for i := 0; i < w; i++ {
			cv.Set(x+i, y+2, '─', fg(cLine))
		}
		top = 3
	}
	lines := append(m.sampleLines(r, w), m.detailBody(r, w)...)
	bh := h - top
	off := 0
	if scroll {
		if m.Offset > max(0, len(lines)-bh) {
			m.Offset = max(0, len(lines)-bh)
		}
		off = m.Offset
	}
	for i := 0; i < bh && off+i < len(lines); i++ {
		cx := x
		for _, sp := range lines[off+i] {
			cx += cv.Text(cx, y+top+i, sp.s, sp.st, x+w-cx)
		}
	}
	if len(lines) > off+bh && bh > 0 {
		more := " ↓ " + itoa(len(lines)-off-bh) + " more "
		cv.Text(x+w-width(more), y+h, more, fg(cFaint), -1)
	}
}
