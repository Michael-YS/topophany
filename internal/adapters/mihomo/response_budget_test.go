package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters"
)

func TestMihomoInventoryCapacityAndIgnoredFieldBounds(t *testing.T) {
	proxies := map[string]any{}
	for i := 0; i <= maxCoreProxies; i++ {
		proxies[fmt.Sprint(i)] = map[string]string{"type": "Direct"}
	}
	tooMany, _ := json.Marshal(map[string]any{"proxies": proxies})
	members := "[" + strings.Repeat(`"node",`, maxCoreProxies) + `"last"]`
	cases := []struct{ endpoint, body, code string }{
		{"/proxies", string(tooMany), "proxy_inventory_capacity_exceeded"},
		{"/proxies", `{"proxies":{"root":{"all":` + members + `}}}`, "proxy_members_capacity_exceeded"},
		{"/providers/proxies", `{"providers":{"p":{"proxies":` + members + `}}}`, "proxy_members_capacity_exceeded"},
		{"/proxies", `{"proxies":{"a":{},"a":{}}}`, "response_complexity_exceeded"},
		{"/proxies", `{"ignored":{"proxies":` + string(tooMany) + `}}`, "response_complexity_exceeded"},
		{"/proxies", `{"proxies":{"root":{"history":[` + strings.Repeat(`{},`, 256) + `{}]}}}`, "response_complexity_exceeded"},
		{"/providers/proxies", `{"providers":{"p":{"proxies":[{"history":[` + strings.Repeat(`{},`, 256) + `{}]}]}}}`, "response_complexity_exceeded"},
		{"/proxies", `{"proxies":{}} {}`, "response_complexity_exceeded"},
		{"/proxies", strings.Repeat(`{"x":`, 34) + `0` + strings.Repeat(`}`, 34), "response_complexity_exceeded"},
	}
	for i, tt := range cases {
		if code := coreResponseBudget([]byte(tt.body), tt.endpoint); code != tt.code {
			t.Errorf("case %d: got %q, want %q", i, code, tt.code)
		}
	}
}

func TestMihomoInventoryByteBudgetIsSeparateFromConfig(t *testing.T) {
	for _, tt := range []struct {
		path string
		size int
		want string
	}{
		{"/proxies", 3 << 20, ""},
		{"/providers/proxies", 3 << 20, ""},
		{"/configs", (2 << 20) + 1, "response_limit_exceeded"},
		{"/proxies", (8 << 20) + 1, "response_limit_exceeded"},
	} {
		t.Run(fmt.Sprintf("%s/%d", tt.path, tt.size), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{}` + strings.Repeat(" ", tt.size-2)))
			}))
			defer s.Close()
			var out map[string]any
			err := readCore(context.Background(), s.Client(), s.URL, tt.path, "", &out)
			code := ""
			if err != nil {
				code = err.(*adapters.Failure).Code
			}
			if code != tt.want {
				t.Fatalf("got %q, want %q", code, tt.want)
			}
		})
	}
}
