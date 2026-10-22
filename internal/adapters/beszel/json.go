package beszel

import "encoding/json"

// decodeObjectFields reads only exact protocol names. encoding/json's struct
// decoder also accepts case-insensitive matches, letting an unknown "ID" or
// "STATS" member overwrite canonical data. Unknown members stay ignored here;
// the caller validates required fields after this representation check.
// The entire response has already passed jsonBudget, including duplicate keys.
func decodeObjectFields(raw []byte, fields map[string]any) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return failure("beszel_json")
	}
	for name, target := range fields {
		if value, present := object[name]; present {
			if json.Unmarshal(value, target) != nil {
				return failure("beszel_json")
			}
		}
	}
	return nil
}

// validUnicodeEscapes rejects lone UTF-16 surrogates before encoding/json can
// replace them with U+FFFD and thereby change a filesystem's hashed identity.
// It leaves JSON syntax checking to the bounded decoder. Escaped backslashes
// are consumed together, so a literal "\\ud800" remains ordinary text.
func validUnicodeEscapes(raw []byte) bool {
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
