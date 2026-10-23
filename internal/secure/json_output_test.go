package secure

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalJSONPreservesValuesAndEscapesControls(t *testing.T) {
	value := strings.Repeat("\u009b\u202e\x7f", 32) + "中文\n"
	for _, indent := range []string{"", "  "} {
		raw, err := MarshalJSON(map[string]string{"id": value}, indent)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range []rune{'\u009b', '\u202e', '\x7f'} {
			if bytes.ContainsRune(raw, r) {
				t.Fatalf("raw control: %q", raw)
			}
		}
		var decoded map[string]string
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded["id"] != value {
			t.Fatalf("roundtrip: %v %q", err, decoded)
		}
		if len(raw) <= len(value) {
			t.Fatal("output budget must include escape expansion")
		}
	}
	if _, err := MarshalJSON(make(chan int), ""); err == nil {
		t.Fatal("unsupported value accepted")
	}
}
