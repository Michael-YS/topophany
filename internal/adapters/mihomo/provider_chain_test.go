package mihomo_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestMihomoProviderOnlyChainAndAmbiguousNames(t *testing.T) {
	for _, tt := range []struct {
		name, mode, providers, outcome, code string
	}{
		{"provider leaf", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks"}]}}`, "ok", ""},
		{"dialer chain", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks","dialer-proxy":"DIRECT"}]}}`, "ok", ""},
		{"mode policy", "global", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks"}]}}`, "policy", "mode_mismatch"},
		{"duplicate names", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks"}]},"q":{"proxies":[{"name":"leaf","type":"VLESS"}]}}`, "unknown", "runtime_chain_unresolved"},
		{"provider cycle", "rule", `{"p":{"proxies":[{"name":"leaf","type":"Shadowsocks","dialer-proxy":"leaf"}]}}`, "unknown", "runtime_chain_unresolved"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/configs":
					_, _ = w.Write([]byte(`{"mode":"` + tt.mode + `"}`))
				case "/proxies":
					_, _ = w.Write([]byte(`{"proxies":{"root":{"type":"Selector","now":"leaf","all":["leaf"]}}}`))
				case "/providers/proxies":
					reads++
					_, _ = w.Write([]byte(`{"providers":` + tt.providers + `}`))
				default:
					t.Error("unreviewed endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			x := httpCheck(s.URL)
			x.Kind = "mihomo"
			x.Adapter = model.AdapterOptions{RootProxy: "root", ExpectedMode: "rule"}
			o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.outcome || o.Code != tt.code || reads != 1 {
				t.Fatalf("provider-only chain: %+v, reads=%d", o, reads)
			}
			if o.Outcome == "ok" && (o.Facts["reported_chain"] == "" || o.Facts["chain_resolution"] != "" || o.Connectivity || o.Evidence != "reported") {
				t.Fatalf("resolved chain lost evidence boundary: %+v", o)
			}
		})
	}
}
