package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestMihomoResponseBudgets(t *testing.T) {
	wide := map[string]any{}
	for i := 0; i < 129; i++ {
		wide[strings.Repeat("k", i%100)+string(rune(0x100+i))] = 1
	}
	raw, _ := json.Marshal(wide)
	array := "[" + strings.Repeat("{},", 256) + "{}]"
	for _, body := range []string{
		`{"proxies":` + string(raw) + `}`, `{"providers":` + string(raw) + `}`,
		`{"proxies":{"p":{"history":` + array + `}}}`, `{"proxies":{"p":{"all":` + array + `}}}`,
		`{"providers":{"p":{"proxies":` + array + `}}}`, `{"mode":"a","mode":"b"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		for _, path := range []string{"/configs", "/proxies", "/providers/proxies"} {
			var v map[string]any
			if err := readCore(context.Background(), srv.Client(), srv.URL, path, "", &v); err == nil {
				t.Fatal("complex response accepted", path)
			}
			if v != nil {
				t.Fatal("destination allocated before budget check")
			}
		}
		srv.Close()
	}
}
