package tui

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"
)

type Decoder struct {
	buffer   []byte
	escapeAt time.Time
	pasting  bool
	paste    []byte
}

var sequences = map[string]string{"\x1b[A": "up", "\x1b[B": "down", "\x1b[C": "right", "\x1b[D": "left", "\x1b[H": "home", "\x1b[F": "end", "\x1b[1~": "home", "\x1b[4~": "end", "\x1b[5~": "pgup", "\x1b[6~": "pgdown", "\x1bOH": "home", "\x1bOF": "end", "\x1b[Z": "shift-tab", "\x1b[3~": "delete"}

// sgrMouse parses an SGR (1006) mouse report "\x1b[<b;x;yM" into
// "mouse:b:x:y:M" (M press, m release). Anything malformed is dropped.
func sgrMouse(seq []byte) (string, bool) {
	if len(seq) < 6 || seq[2] != '<' {
		return "", false
	}
	final := seq[len(seq)-1]
	parts := bytes.Split(seq[3:len(seq)-1], []byte(";"))
	if (final != 'M' && final != 'm') || len(parts) != 3 {
		return "", false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 5 {
			return "", false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return "", false
			}
		}
	}
	return "mouse:" + string(parts[0]) + ":" + string(parts[1]) + ":" + string(parts[2]) + ":" + string(final), true
}

// cellSize parses a reply to CSI 16 t, "\x1b[6;h;wt", into "cell:h:w": a
// cell's height and width in pixels. Anything else is not one.
func cellSize(seq []byte) (string, bool) {
	if len(seq) < 8 || seq[len(seq)-1] != 't' {
		return "", false
	}
	parts := bytes.Split(seq[2:len(seq)-1], []byte(";"))
	if len(parts) != 3 || string(parts[0]) != "6" {
		return "", false
	}
	for _, p := range parts[1:] {
		if len(p) == 0 || len(p) > 4 {
			return "", false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return "", false
			}
		}
	}
	return "cell:" + string(parts[1]) + ":" + string(parts[2]), true
}

func (d *Decoder) Feed(b []byte, now time.Time) []string {
	d.buffer = append(d.buffer, b...)
	keys := []string{}
	for len(d.buffer) > 0 {
		if d.pasting {
			end := bytes.Index(d.buffer, []byte("\x1b[201~"))
			if end < 0 {
				// Keep only a delimiter-sized suffix. Excess paste data is discarded
				// as paste, never reinterpreted as navigation/quit commands.
				take := max(0, len(d.buffer)-5)
				keep := min(take, max(0, 8192-len(d.paste)))
				d.paste = append(d.paste, d.buffer[:keep]...)
				d.buffer = append([]byte(nil), d.buffer[take:]...)
				break
			}
			keep := min(end, max(0, 8192-len(d.paste)))
			d.paste = append(d.paste, d.buffer[:keep]...)
			keys = append(keys, "paste:"+strings.ToValidUTF8(string(d.paste), "�"))
			d.paste = nil
			d.pasting = false
			d.buffer = d.buffer[end+6:]
			continue
		}
		if bytes.HasPrefix(d.buffer, []byte("\x1b[200~")) {
			d.pasting = true
			d.escapeAt = time.Time{}
			d.buffer = d.buffer[6:]
			continue
		}
		if d.buffer[0] == 27 {
			if d.escapeAt.IsZero() {
				d.escapeAt = now
			}
			matched := false
			partial := false
			for seq, key := range sequences {
				if bytes.HasPrefix(d.buffer, []byte(seq)) {
					keys = append(keys, key)
					d.buffer = d.buffer[len(seq):]
					matched = true
					break
				}
				if strings.HasPrefix(seq, string(d.buffer)) {
					partial = true
				}
			}
			if matched {
				d.escapeAt = time.Time{}
				continue
			}
			if strings.HasPrefix("\x1b[200~", string(d.buffer)) {
				partial = true
			}
			if partial && now.Sub(d.escapeAt) < 80*time.Millisecond {
				break
			}
			if len(d.buffer) > 1 && d.buffer[1] == '[' {
				end := -1
				for i := 2; i < len(d.buffer); i++ {
					if d.buffer[i] >= 0x40 && d.buffer[i] <= 0x7e {
						end = i
						break
					}
				}
				if end >= 0 {
					if k, ok := sgrMouse(d.buffer[:end+1]); ok {
						keys = append(keys, k)
					} else if k, ok := cellSize(d.buffer[:end+1]); ok {
						keys = append(keys, k)
					}
					d.buffer = d.buffer[end+1:]
					d.escapeAt = time.Time{}
					continue
				}
				if now.Sub(d.escapeAt) < 80*time.Millisecond {
					break
				}
				d.buffer = nil
			} else {
				keys = append(keys, "esc")
				d.buffer = d.buffer[1:]
			}
			d.escapeAt = time.Time{}
			continue
		}
		c := d.buffer[0]
		switch c {
		case 3:
			keys = append(keys, "ctrl-c")
			d.buffer = d.buffer[1:]
		case 9:
			keys = append(keys, "tab")
			d.buffer = d.buffer[1:]
		case 19:
			keys = append(keys, "ctrl-s")
			d.buffer = d.buffer[1:]
		case 10, 13:
			keys = append(keys, "enter")
			d.buffer = d.buffer[1:]
		case 8, 127:
			keys = append(keys, "backspace")
			d.buffer = d.buffer[1:]
		default:
			if c < 32 {
				d.buffer = d.buffer[1:]
				continue
			}
			if !utf8.FullRune(d.buffer) {
				return keys
			}
			r, n := utf8.DecodeRune(d.buffer)
			keys = append(keys, string(r))
			d.buffer = d.buffer[n:]
		}
	}
	return keys
}
