package secure

import "unicode/utf8"

// ValidJSONUnicode refuses invalid UTF-8 and lone UTF-16 surrogate escapes
// before encoding/json can silently replace them with U+FFFD. Callers still
// need a JSON syntax/complexity check. Escaped backslashes are consumed in
// pairs, so a literal "\\ud800" remains ordinary text.
func ValidJSONUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		v, ok := hexQuad(raw[i+1:])
		if !ok {
			return false
		}
		i += 4
		switch {
		case v >= 0xdc00 && v <= 0xdfff:
			return false
		case v >= 0xd800 && v <= 0xdbff:
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, ok := hexQuad(raw[i+3:])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func hexQuad(raw []byte) (uint16, bool) {
	if len(raw) < 4 {
		return 0, false
	}
	var v uint16
	for _, c := range raw[:4] {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint16(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint16(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			v |= uint16(c - 'A' + 10)
		default:
			return 0, false
		}
	}
	return v, true
}
