package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestPrivateHTTPStatusIsWithheld(t *testing.T) {
	for _, mode := range []string{"public", "bearer", "header", "host", "query", "proxy-password"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("NUMERIC_TEST_SECRET", "527")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(527) }))
			defer server.Close()
			c := testkit.Config()
			x := httpCheck(server.URL)
			switch mode {
			case "bearer":
				x.HTTP.BearerEnv = "NUMERIC_TEST_SECRET"
			case "header":
				x.HTTP.RequestHeaders = map[string]string{"X-Private": "527"}
			case "host":
				x.HTTP.RequestHeaders = map[string]string{"Host": "527"}
			case "query":
				x.Target += "?token=527"
			case "proxy-password":
				x.Path = "direct"
				c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: connectFixture(t, "", "527", nil), PasswordEnv: "NUMERIC_TEST_SECRET"}}
			}
			o := (Runner{}).Run(context.Background(), c, x)
			if o.Outcome != "fail" || o.Code != "unexpected_status" || !o.Connectivity || o.Facts["response_proof"] != "http_response" {
				t.Fatal(o)
			}
			if mode == "public" {
				if o.Facts["http_status"] != "527" {
					t.Fatal("public status lost", o)
				}
				return
			}
			if o.Facts["http_status"] != "" || o.Facts["peer_text"] != withheld {
				t.Fatal("private status retained", o)
			}
			// Ignore envelope hashes and timing fields, which can coincidentally contain
			// short digit strings; the complete peer-supplied fact must be absent.
			raw, _ := json.Marshal(o.Facts)
			if strings.Contains(string(raw), "527") {
				t.Fatalf("numeric credential in facts: %s", raw)
			}
		})
	}
}

func TestPrivateMihomoNumericTelemetryIsWithheld(t *testing.T) {
	for _, mode := range []string{"public", "token", "proxy-password"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("NUMERIC_TEST_SECRET", "527")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/configs":
					fmt.Fprint(w, `{"mode":"rule","tun":{"enable":true}}`)
				case "/proxies":
					fmt.Fprint(w, `{"proxies":{"root":{"type":"Selector","now":"node","history":[{"time":"2025-01-01T00:08:47Z","delay":527}]},"node":{"type":"VLESS"}}}`)
				case "/providers/proxies":
					fmt.Fprint(w, `{"providers":{"pool":{"proxies":[{"name":"node"}],"updatedAt":"2025-01-01T00:08:47Z"}}}`)
				}
			}))
			defer server.Close()
			c := testkit.Config()
			x := httpCheck(server.URL)
			x.Kind = "mihomo"
			x.Adapter = model.AdapterOptions{RootProxy: "root", ExpectedProviders: map[string][]string{"pool": {"node"}}}
			if mode == "token" {
				x.Adapter.TokenEnv = "NUMERIC_TEST_SECRET"
			}
			if mode == "proxy-password" {
				x.Path = "direct"
				c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: connectFixture(t, "", "527", nil), PasswordEnv: "NUMERIC_TEST_SECRET"}}
			}
			o := (Runner{}).Run(context.Background(), c, x)
			if o.Outcome != "ok" || o.Facts["reported_selection"] != "node" {
				t.Fatal(o)
			}
			for _, key := range []string{"core_delay_ms", "core_last_checked", "provider_updated_at.pool"} {
				if (o.Facts[key] != "") != (mode == "public") {
					t.Fatal(mode, key, o)
				}
			}
			if mode != "public" && o.Facts["provider_comparison"] != "pool=match" {
				t.Fatal("raw numeric comparison exported", o)
			}
		})
	}
	// A nonstandard API status must not reflect a numeric credential through Code.
	t.Setenv("NUMERIC_TEST_SECRET", "527")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(527) }))
	defer server.Close()
	x := httpCheck(server.URL)
	x.Kind = "mihomo"
	x.Adapter.TokenEnv = "NUMERIC_TEST_SECRET"
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Code != "unexpected_status" || strings.Contains(o.Detail, "527") {
		t.Fatal(o)
	}
}
