package secure

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// MarshalJSON preserves JSON string values while escaping terminal control
// and bidi runes which encoding/json otherwise writes literally. An empty
// indent selects compact output; callers own framing and the final byte budget.
func MarshalJSON(v any, indent string) ([]byte, error) {
	var raw []byte
	var err error
	if indent == "" {
		raw, err = json.Marshal(v)
	} else {
		raw, err = json.MarshalIndent(v, "", indent)
	}
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	out.Grow(len(raw))
	for _, r := range string(raw) {
		if r != '\n' && unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return []byte(out.String()), nil
}
