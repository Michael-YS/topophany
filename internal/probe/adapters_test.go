package probe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
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
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
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
	if o.Facts["core_last_checked"] != "2025-01-01T00:00:00Z" {
		t.Fatal("historical kernel timestamp replaced with sampling time")
	}
	if !strings.Contains(o.PathEvidence, "not_request_verified") {
		t.Fatal(o)
	}
	// Sent no secret, it names the chain as it is, nearest hop first.
	mu.Lock()
	auth = ""
	mu.Unlock()
	x.Adapter.TokenEnv = ""
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
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
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	b, _ := json.Marshal(o)
	for _, part := range []string{token[:12], token[12:], "9f8e7d"} {
		if strings.Contains(string(b), part) {
			t.Fatalf("observation carries %q: %s", part, b)
		}
	}
	if o.Outcome != "policy" || o.Facts["mode"] != "other" || o.Facts["reported_chain"] != "undeclared" || o.Facts["reported_selection"] != "undeclared" || o.Facts["provider.airport"] != "node-a,undeclared" || o.Facts["test_url"] != "" || o.Facts["peer_text"] != withheld {
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
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.want || o.Code != tt.code {
				t.Fatal(o)
			}
		})
	}
}
func TestMihomoForbiddenReadAndUnknownSchema(t *testing.T) {
	if e := readCore(context.Background(), http.DefaultClient, "http://127.0.0.1:1", "/group/auto/delay", "", &map[string]any{}); e == nil {
		t.Fatal("side-effecting GET allowed")
	}
	for _, body := range []string{`not-json`, `{"tun":{}}`, strings.Repeat("x", (2<<20)+1)} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		x := httpCheck(s.URL)
		x.Kind = "mihomo"
		o := (Runner{}).Run(context.Background(), testkit.Config(), x)
		s.Close()
		if o.Outcome != "error" {
			t.Fatal("unknown API shape invented success", o)
		}
	}
}
func TestProxyGraphCyclesUnknownGroupsAndOpaqueProvider(t *testing.T) {
	all := map[string]coreProxy{"a": {Type: "VLESS", DialerProxy: "b"}, "b": {Type: "VLESS", DialerProxy: "a"}}
	if _, e := expandProxy(all, "a"); e == nil {
		t.Fatal("dialer cycle accepted")
	}
	all = map[string]coreProxy{"a": {Type: "Selector", Now: "missing"}}
	if _, e := expandProxy(all, "a"); e == nil {
		t.Fatal("missing provider invented")
	}
	all = map[string]coreProxy{"a": {Type: "UnsupportedGroup", All: []string{"b"}}}
	if _, e := expandProxy(all, "a"); e == nil {
		t.Fatal("unknown group guessed")
	}
	all = map[string]coreProxy{"owned-exit": {Type: "VLESS", DialerProxy: "airport-choice"}, "airport-choice": {Type: "Selector", Now: "airport-node"}, "airport-node": {Type: "Shadowsocks"}}
	chain, e := expandProxy(all, "owned-exit")
	if e != nil || !reflect.DeepEqual(chain, []string{"airport-node", "owned-exit"}) {
		t.Fatal("external provider chain reversed", chain, e)
	}
	if chain, e = expandProxy(all, "DIRECT"); e != nil || len(chain) != 1 {
		t.Fatal(e)
	}
}
func TestTailscaleIdleOfflineAndScope(t *testing.T) {
	for _, tt := range []struct{ name, online, active, cur, relay, want, hint string }{{"idle is not offline", "true", "false", "127.0.0.1:41641", "", "ok", "direct_endpoint_reported"}, {"DERP preference is not request proof", "true", "true", "", "hkg", "ok", "relay_preference_reported"}, {"offline report not power state", "false", "false", "", "", "unknown", "unknown"}} {
		t.Run(tt.name, func(t *testing.T) {
			r := Runner{Command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name != "tailscale" || !reflect.DeepEqual(args, []string{"status", "--json"}) {
					t.Fatal("unexpected command", name, args)
				}
				return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer-1","HostName":"dmit","TailscaleIPs":["100.64.0.1"],"Online":` + tt.online + `,"Active":` + tt.active + `,"CurAddr":"` + tt.cur + `","Relay":"` + tt.relay + `"}}}`), nil
			}}
			x := httpCheck("")
			x.Kind = "tailscale"
			x.Adapter.Peer = "100.64.0.1"
			o := r.Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.want || o.Evidence != "reported" || o.Facts["transport_hint"] != tt.hint || o.Connectivity {
				t.Fatal(o)
			}
		})
	}
	x := httpCheck("")
	x.Kind = "tailscale"
	x.Adapter.Peer = "missing"
	r := Runner{Command: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Peer":{}}`), nil
	}}
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "unknown" || o.Code != "peer_not_found" {
		t.Fatal(o)
	}
	r.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("secret raw stderr") }
	o := r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || strings.Contains(o.Detail, "secret") {
		t.Fatal(o)
	}
}
func TestWireGuardOldHandshakeIsNotFailureAndKeysAreDiscarded(t *testing.T) {
	x := httpCheck("")
	x.Kind = "wireguard"
	x.Adapter = model.AdapterOptions{Interface: "wg0", Peer: "PUBLIC-PEER"}
	r := Runner{Command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "wg" || !reflect.DeepEqual(args, []string{"show", "wg0", "dump"}) {
			t.Fatal("unexpected command", name, args)
		}
		return []byte("INTERFACE-PRIVATE-KEY interface-public 51820 off\nPUBLIC-PEER PRESHARED-SECRET 192.0.2.1:51820 10.0.0.0/24 100 123 456 0\n"), nil
	}}
	o := r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Connectivity || o.Evidence != "reported" || o.Facts["rx_bytes"] != "123" {
		t.Fatal(o)
	}
	if o.Facts["last_handshake"] != time.Unix(100, 0).UTC().Format(time.RFC3339) {
		t.Fatal("old handshake replaced with fresh timestamp")
	}
	b, _ := json.Marshal(o)
	if strings.Contains(string(b), "PRIVATE-KEY") || strings.Contains(string(b), "PRESHARED-SECRET") {
		t.Fatal("WireGuard private key leaked")
	}
	x.Adapter.Peer = "missing"
	if o = r.Run(context.Background(), testkit.Config(), x); o.Outcome != "unknown" {
		t.Fatal(o)
	}
}
func TestAdapterExecutableAndOutputBudget(t *testing.T) {
	if _, e := defaultCommand(context.Background(), "sh", "-c", "anything"); e == nil {
		t.Fatal("arbitrary command accepted")
	}
	w := &cappedOutput{limit: 3}
	if _, e := w.Write([]byte("abc")); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Write([]byte("x")); e == nil {
		t.Fatal("command output unbounded")
	}
	x := httpCheck("")
	x.Kind = "wireguard"
	x.Adapter = model.AdapterOptions{Interface: "wg0", Peer: "peer"}
	r := Runner{Command: func(context.Context, string, ...string) ([]byte, error) { return []byte("bad"), nil }}
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "error" {
		t.Fatal(o)
	}
	r.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no permission") }
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "error" {
		t.Fatal(o)
	}
}
