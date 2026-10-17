package probe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestExitJSONComplexity(t *testing.T) {
	wide := map[string]any{"ip": "203.0.113.8"}
	for i := 0; i < 17; i++ {
		wide[strings.Repeat("k", i+1)] = 1
	}
	raw, _ := json.Marshal(wide)
	for _, body := range []string{string(raw), `{"ip":"203.0.113.8","ip":"203.0.113.9"}`, `{"ip":"203.0.113.8","ignored":` + strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33) + "}"} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			t.Parallel()
			if _, err := exitIP([]byte(body), model.HTTPOptions{ExitFormat: "json", ExitJSONKey: "ip"}); err == nil {
				t.Fatal("hostile JSON accepted")
			}
		})
	}
	if _, err := exitIP([]byte(`{"ip":"203.0.113.8"}`), model.HTTPOptions{ExitFormat: "json", ExitJSONKey: "ip"}); err != nil {
		t.Fatal(err)
	}
}
