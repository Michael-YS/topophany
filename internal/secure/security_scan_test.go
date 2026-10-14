package secure

import (
	"strings"
	"testing"
	"unicode"
)

func TestAllBidiControlsAreRemoved(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.Is(unicode.Bidi_Control, r) && Text("safe"+string(r)+"label") != "safelabel" {
			t.Fatalf("bidi control U+%04X survived", r)
		}
	}
	if got := Text("长沙 · café العربية"); got != "长沙 · café العربية" {
		t.Fatal("safe multilingual text damaged", got)
	}
}

func TestJSONBudgetStopsCompactExpansion(t *testing.T) {
	for _, s := range []string{
		`[` + strings.Repeat(`{},`, 4096) + `{}]`,
		strings.Repeat(`[`, 34) + `0` + strings.Repeat(`]`, 34),
		`{"a":1,"a":2}`,
		`{"a":"` + strings.Repeat("x", 4097) + `"}`,
		`{} {}`,
	} {
		if err := JSONBudget([]byte(s)); err == nil {
			t.Fatal("hostile JSON accepted")
		}
	}
	if err := JSONBudget([]byte(`{"items":[{},1,"汉字",null,true]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRejectsStructureBeforeAllocatingDestination(t *testing.T) {
	var target struct {
		Observations []struct{ Facts map[string]string } `json:"observations"`
	}
	raw := `{"observations":[` + strings.Repeat(`{},`, 50000) + `{}]}`
	if err := Decode(strings.NewReader(raw), &target, 1<<20); err == nil {
		t.Fatal("compact input bypassed structural budget")
	}
	if target.Observations != nil {
		t.Fatal("oversized representation was allocated before rejection")
	}
}
