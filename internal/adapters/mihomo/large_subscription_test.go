package mihomo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestMihomoLargeSubscriptionKeepsSelectionAndProviderPolicy(t *testing.T) {
	for _, count := range []int{1000, 4095} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			proxies := map[string]any{}
			members := make([]string, count)
			for i := range members {
				members[i] = fmt.Sprintf("node-%04d", i)
				proxies[members[i]] = map[string]any{"type": "Shadowsocks", "name": members[i]}
			}
			proxies["root"] = map[string]any{"type": "Selector", "now": members[count-1], "all": members}
			proxyBody, _ := json.Marshal(map[string]any{"proxies": proxies})
			providerMembers := make([]map[string]string, count)
			for i, name := range members {
				providerMembers[i] = map[string]string{"name": name}
			}
			providerBody, _ := json.Marshal(map[string]any{"providers": map[string]any{
				"large":   map[string]any{"proxies": providerMembers},
				"checked": map[string]any{"proxies": []map[string]string{{"name": "expected"}}},
				"default": map[string]any{"proxies": []any{proxies["root"]}},
			}})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("non-read request")
				}
				switch r.URL.Path {
				case "/configs":
					_, _ = w.Write([]byte(`{"mode":"rule","tun":{"enable":true}}`))
				case "/proxies":
					_, _ = w.Write(proxyBody)
				case "/providers/proxies":
					_, _ = w.Write(providerBody)
				default:
					t.Error("unreviewed endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			x := httpCheck(s.URL)
			x.Kind, x.TimeoutSeconds = "mihomo", 10
			x.Adapter = model.AdapterOptions{RootProxy: "root", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"checked": {"expected"}}}
			o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != "ok" || o.Evidence != "reported" || o.Connectivity || o.Facts["reported_chain"] != members[count-1] || !strings.Contains(o.Facts["provider_comparison"], "checked=match") {
				t.Fatalf("large inventory lost selection, policy or evidence: %+v", o)
			}
			x.Adapter.ExpectedProviders["checked"] = []string{"different"}
			o = (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != "policy" || o.Code != "provider_membership_mismatch" {
				t.Fatalf("large inventory masked drift: %+v", o)
			}
		})
	}
}
