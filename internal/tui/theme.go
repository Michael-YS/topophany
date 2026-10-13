package tui

// Palette: a dark "phosphor" scheme. Backgrounds stay on the terminal default
// except for selection, keycaps and modals, so light terminals remain legible.
var (
	cAccent  = Hex(0x5EEAD4) // teal phosphor
	cAccent2 = Hex(0x7DD3FC) // sky
	cGlow    = Hex(0xF0FDFA)
	cText    = Hex(0xD7DEE6)
	cMuted   = Hex(0x8B98A9)
	cFaint   = Hex(0x4B5565)
	cLine    = Hex(0x334155)
	cUp      = Hex(0x4ADE80)
	cDown    = Hex(0xF87171)
	cWarn    = Hex(0xFBBF24)
	cUnknown = Hex(0x7C8799)
	cSelBG   = Hex(0x16323A)
	cPanelBG = Hex(0x0E1A22)
	cKeyBG   = Hex(0x1E3A44)
	cLand    = Hex(0x2E7268)
	cCoast   = Hex(0x4FA597)
	cOcean   = Hex(0x1C3042)
	cDeep    = Hex(0x0B1620)
	cEdit    = Hex(0xC4B5FD)
)

var groupHues = []Color{Hex(0x7DD3FC), Hex(0xF0ABFC), Hex(0xFCD34D), Hex(0x86EFAC), Hex(0xFDA4AF), Hex(0xA5B4FC), Hex(0x5EEAD4), Hex(0xFDBA74)}

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
