package mihomo_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestMihomoReadsOnlySafeEndpointsAndExpandsDialerProxy(t *testing.T) {
	t.Setenv("TOPH_CORE_TOKEN", "private-control-token")
	var mu sync.Mutex
	calls := []string{}
	auth := "Bearer private-control-token"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		want := auth
		mu.Unlock()
		if r.Method != "GET" || r.Header.Get("Authorization") != want {
			t.Error("wrong method or auth")
		}
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"rule","tun":{"enable":true},"secret":"must-not-leak"}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{"residential":{"type":"Fallback","now":"aaitr","testUrl":"https://example.invalid/204?secret=private","history":[{"time":"2025-01-01T00:00:00Z","delay":123}]},"aaitr":{"type":"VLESS","dialer-proxy":"entry"},"entry":{"type":"Selector","now":"s2"},"s2":{"type":"VLESS"}}}`)
		case "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{"airport":{"proxies":[{"name":"node-a","password":"not-for-export"},{"name":"node-b"}]}}}`)
		default:
			t.Error("unsafe API called", r.URL.Path)
			http.Error(w, "forbidden", 500)
		}
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.Kind = "mihomo"
	x.Adapter = model.AdapterOptions{TokenEnv: "TOPH_CORE_TOKEN", RootProxy: "residential", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"airport": {"node-a", "node-b"}}}
	o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	// Sent a token, the controller names only what the topology declares.
	if o.Outcome != "ok" || o.Evidence != "reported" || o.Connectivity || o.Facts["reported_chain"] != "undeclared -> undeclared" || o.Facts["provider.airport"] != "node-a,node-b" {
		t.Fatal("runtime report promoted to measured path, or undeclared names kept", o)
	}
	if o.Facts["provider_parameters"] != "not_verifiable_by_names_alone" {
		t.Fatal("provider names claimed parameter verification")
	}
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"GET /configs", "GET /proxies", "GET /providers/proxies"}) {
		t.Fatal(got)
	}
	b, _ := json.Marshal(o)
	for _, secret := range []string{"must-not-leak", "not-for-export", "private-control-token", "secret=private"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("adapter exported secret", secret)
		}
	}
	if o.Facts["core_last_checked"] != "" || o.Facts["core_delay_ms"] != "" {
		t.Fatal("credential-bearing controller exported history telemetry")
	}
	if !strings.Contains(o.PathEvidence, "not_request_verified") {
		t.Fatal(o)
	}
	// Sent no secret, it names the chain as it is, nearest hop first.
	mu.Lock()
	auth = ""
	mu.Unlock()
	x.Adapter.TokenEnv = ""
	o = (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Facts["core_last_checked"] != "2025-01-01T00:00:00Z" || o.Facts["core_delay_ms"] != "123" {
		t.Fatal("public controller lost historical telemetry")
	}
	if o.Outcome != "ok" || o.Facts["reported_chain"] != "s2 -> aaitr" || o.Facts["reported_selection"] != "aaitr" || o.Facts["test_url"] != "https://example.invalid/204?redacted" {
		t.Fatal("reverse chain", o)
	}
}

// A controller sent a token can hand it back in anything it answers. The
// observation, which every viewer of a collector reads, keeps its mode in
// a closed word, names only what the topology declares and leaves out its
// test URL, so no part of the token reaches it.
func TestMihomoSentATokenCannotEchoIt(t *testing.T) {
	const token = "ctl-9f8e7d6c5b4a39281706"
	t.Setenv("TOPH_CORE_TOKEN", token)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/configs":
			_, _ = io.WriteString(w, `{"mode":"`+token+`","tun":{"enable":true}}`)
		case "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{"root":{"type":"Selector","now":"`+token[:12]+`","testUrl":"https://`+token+`.example.invalid/`+token+`"},"`+token[:12]+`":{"type":"Selector","now":"`+token[12:]+`"},"`+token[12:]+`":{"type":"VLESS"}}}`)
		case "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{"airport":{"proxies":[{"name":"node-a"},{"name":"`+token+`"}]}}}`)
		}
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.Kind = "mihomo"
	x.Adapter = model.AdapterOptions{TokenEnv: "TOPH_CORE_TOKEN", RootProxy: "root", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"airport": {"node-a", "node-b"}}}
	o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	b, _ := json.Marshal(o)
	for _, part := range []string{token[:12], token[12:], "9f8e7d"} {
		if strings.Contains(string(b), part) {
			t.Fatalf("observation carries %q: %s", part, b)
		}
	}
	if o.Outcome != "policy" || o.Facts["mode"] != "other" || o.Facts["reported_chain"] != "undeclared" || o.Facts["reported_selection"] != "undeclared" || o.Facts["provider.airport"] != "node-a,undeclared" || o.Facts["test_url"] != "" || o.Facts["peer_text"] != adapters.Withheld {
		t.Fatal(o)
	}
}
func TestMihomoModeProviderDriftAndAPIAuth(t *testing.T) {
	for _, tt := range []struct {
		name, mode, proxies, providers string
		status                         int
		want, code                     string
	}{{"wrong mode", "global", `{"proxies":{"leaf":{"type":"VLESS"}}}`, `{"providers":{"p":{"proxies":[{"name":"new"}]}}}`, 200, "policy", "mode_mismatch"}, {"provider not loaded", "rule", `{"proxies":{"leaf":{"type":"VLESS"}}}`, `{"providers":{"p":{"proxies":[{"name":"old"}]}}}`, 200, "policy", "provider_membership_mismatch"}, {"empty load-balanced selection", "rule", `{"proxies":{"leaf":{"type":"LoadBalance","all":["x"]}}}`, `{"providers":{"p":{"proxies":[{"name":"new"}]}}}`, 200, "unknown", "runtime_chain_unresolved"}, {"mode violation retained when chain unresolved", "global", `{"proxies":{"leaf":{"type":"LoadBalance","all":["x"]}}}`, `{"providers":{"p":{"proxies":[{"name":"new"}]}}}`, 200, "policy", "mode_mismatch"}, {"bad core secret", "rule", `{}`, `{}`, 401, "error", "authentication_failed"}} {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.status != 200 {
					w.WriteHeader(tt.status)
					return
				}
				switch r.URL.Path {
				case "/configs":
					_, _ = io.WriteString(w, `{"mode":"`+tt.mode+`"}`)
				case "/proxies":
					_, _ = io.WriteString(w, tt.proxies)
				case "/providers/proxies":
					_, _ = io.WriteString(w, tt.providers)
				default:
					t.Error("unexpected read")
				}
			}))
			defer s.Close()
			x := httpCheck(s.URL)
			x.Kind = "mihomo"
			x.Adapter = model.AdapterOptions{RootProxy: "leaf", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"p": {"new"}}}
			o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.want || o.Code != tt.code {
				t.Fatal(o)
			}
		})
	}
}
func TestMihomoForbiddenReadAndUnknownSchema(t *testing.T) {

	for _, body := range []string{`not-json`, `{"tun":{}}`, strings.Repeat("x", (2<<20)+1)} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		x := httpCheck(s.URL)
		x.Kind = "mihomo"
		o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
		s.Close()
		if o.Outcome != "error" {
			t.Fatal("unknown API shape invented success", o)
		}
	}
}
func httpCheck(url string) model.Check {
	return model.Check{ID: "check", Observer: "home", Kind: "http", Target: url, TimeoutSeconds: 1, HTTP: model.HTTPOptions{Method: "GET", MaxBodyBytes: 65536}}
}
