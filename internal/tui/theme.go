package tui

import (
	"os"
	"strings"
)

// Palette: "ink and phosphor". The UI paints its own ink background so the
// designed contrast holds on dark, light and tinted terminals alike; set
// TOPO_BACKGROUND=terminal to keep the terminal's own background instead.
// Colors that fade "into the background" always blend toward cInk.
var (
	cInk      = Hex(0x0A1016) // app background
	cInkHi    = Hex(0x0F1820) // header and footer bars
	cSelBG    = Hex(0x112C34) // selected row
	cKeyBG    = Hex(0x1A2B36) // keycaps, chips, form fields
	cPanelBG  = Hex(0x0D151C) // modals
	cLine     = Hex(0x243443) // panel borders
	cText     = Hex(0xD5DEE7)
	cMuted    = Hex(0x8393A3)
	cFaint    = Hex(0x4A5868)
	cGlow     = Hex(0xF0FDFA)
	cAccent   = Hex(0x5EEAD4) // teal phosphor
	cAccent2  = Hex(0x7DD3FC) // sky
	cEdit     = Hex(0xC4B5FD) // local draft / synthetic preview
	cUp       = Hex(0x4ADE80)
	cWarn     = Hex(0xFBBF24)
	cDown     = Hex(0xF87171)
	cUnknown  = Hex(0x6B7A8C)
	cLandFill = Hex(0x11232B) // map land shading (cell background)
	cLand     = Hex(0x1E4149) // map land lattice
	cCoast    = Hex(0x2E636B) // map coastline
	cOcean    = Hex(0x121F29) // map graticule
	cDeep     = cInk
)

var groupHues = []Color{Hex(0x7DD3FC), Hex(0xF0ABFC), Hex(0xFCD34D), Hex(0x86EFAC), Hex(0xFDA4AF), Hex(0xA5B4FC), Hex(0x5EEAD4), Hex(0xFDBA74)}

// paintBackground reports whether the canvas fills unstyled cells with cInk.
func paintBackground() bool {
	v := strings.ToLower(os.Getenv("TOPO_BACKGROUND"))
	return v != "terminal" && v != "none" && v != "transparent"
}

func healthColor(h string) Color {
	switch h {
	case "up":
		return cUp
	case "down":
		return cDown
	case "degraded":
		return cWarn
	}
	return cUnknown
}
func healthGlyph(h string) rune {
	switch h {
	case "up":
		return '●'
	case "down":
		return '✖'
	case "degraded":
		return '▲'
	}
	return '○'
}
func badge(health string) string {
	switch health {
	case "up":
		return "● UP  "
	case "down":
		return "✖ DOWN"
	case "degraded":
		return "▲ WARN"
	default:
		return "○ UNK "
	}
}

// healthWord is the short status label used in tables and pills.
func healthWord(h string) string {
	switch h {
	case "up":
		return "up"
	case "down":
		return "down"
	case "degraded":
		return "warn"
	}
	return "unknown"
}
