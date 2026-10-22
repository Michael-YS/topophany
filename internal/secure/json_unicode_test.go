package secure

import "testing"

func TestValidJSONUnicodePreservesRepresentation(t *testing.T) {
	for _, raw := range []string{`"plain"`, `"探针"`, `"\u4e2d"`, `"\ud83d\ude80"`, `"\\ud800"`, `"\\\ud83d\ude80"`} {
		if !ValidJSONUnicode([]byte(raw)) {
			t.Errorf("valid Unicode refused: %q", raw)
		}
	}
	for _, raw := range []string{"\"\xff\"", `"\ud800"`, `"\udfff"`, `"\ud800\u0041"`, `"\ud800\ud800"`, `"\ud800\\udfff"`, `"\uxxxx"`, `"\u12`, `"\`} {
		if ValidJSONUnicode([]byte(raw)) {
			t.Errorf("lossy Unicode accepted: %q", raw)
		}
	}
}
