package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Captured before the package split; named semantic regressions update only
// their observations. Legacy contract hashes remain pinned.
// Only the listener address, sampling timestamp and elapsed time are variable.
func TestAdapterCompatibility(t *testing.T) {
	t.Setenv("TOPH_COMPAT_TOKEN", "synthetic-token")
	cases := []struct {
		name, kind, mode, proxies, providers, command string
		status                                        int
		commandError                                  bool
	}{
		{name: "mihomo-ok", kind: "mihomo", mode: "rule"},
		{name: "mihomo-mode", kind: "mihomo", mode: "global"},
		{name: "mihomo-provider", kind: "mihomo", mode: "rule", providers: `{"providers":{"pool":{"proxies":[{"name":"changed"}]}}}`},
		{name: "mihomo-chain", kind: "mihomo", mode: "rule", proxies: `{"proxies":{"root":{"type":"Selector","now":"missing"}}}`},
		{name: "mihomo-auth", kind: "mihomo", status: 401},
		{name: "mihomo-http", kind: "mihomo", status: 503},
		{name: "mihomo-shape", kind: "mihomo", mode: ""},
		{name: "tailscale-idle", kind: "tailscale", command: `{"BackendState":"Running","Peer":{"peer":{"Online":true,"Active":false,"Relay":"hkg"}}}`},
		{name: "tailscale-offline", kind: "tailscale", command: `{"BackendState":"Stopped","Peer":{"peer":{"Online":false}}}`},
		{name: "tailscale-missing", kind: "tailscale", command: `{"BackendState":"Running","Peer":{}}`},
		{name: "tailscale-permission", kind: "tailscale", commandError: true},
		{name: "wireguard-ok", kind: "wireguard", command: "PRIVATE interface-public 51820 off\npeer PRESHARED 192.0.2.1:51820 10.0.0.0/24 100 123 456 0\n"},
		{name: "wireguard-missing", kind: "wireguard", command: "PRIVATE interface-public 51820 off\n"},
		{name: "wireguard-shape", kind: "wireguard", command: "bad"},
		{name: "wireguard-permission", kind: "wireguard", commandError: true},
	}
	type record struct {
		Contract    string            `json:"contract"`
		Observation model.Observation `json:"observation"`
	}
	got := map[string]record{}
	for _, tc := range cases {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.status != 0 {
				w.WriteHeader(tc.status)
				return
			}
			switch r.URL.Path {
			case "/configs":
				json.NewEncoder(w).Encode(map[string]any{"mode": tc.mode, "tun": map[string]bool{"enable": true}})
			case "/proxies":
				body := tc.proxies
				if body == "" {
					body = `{"proxies":{"root":{"type":"Selector","now":"node"},"node":{"type":"VLESS"}}}`
				}
				w.Write([]byte(body))
			case "/providers/proxies":
				body := tc.providers
				if body == "" {
					body = `{"providers":{"pool":{"proxies":[{"name":"node"}]}}}`
				}
				w.Write([]byte(body))
			default:
				t.Errorf("unexpected API path: %s", r.URL.Path)
			}
		}))
		x := httpCheck(s.URL)
		x.Kind = tc.kind
		x.Adapter = model.AdapterOptions{TokenEnv: "TOPH_COMPAT_TOKEN", RootProxy: "root", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"pool": {"node"}}, Peer: "peer", Interface: "wg0"}
		r := Runner{Command: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if tc.commandError {
				return nil, errors.New("private diagnostic")
			}
			if tc.kind == "wireguard" {
				if tc.name == "wireguard-ok" {
					if len(args) == 3 && args[2] == "latest-handshakes" {
						return []byte("peer\t100\n"), nil
					}
					return []byte("peer\t123\t456\n"), nil
				}
				if tc.name == "wireguard-missing" {
					return nil, nil
				}
			}
			return []byte(tc.command), nil
		}}
		c := testkit.Config()
		o := r.Run(context.Background(), c, x)
		s.Close()
		if o.Contract != config.Contract(c, x) || o.SampledAt.IsZero() || o.SampledAt.Location() != time.UTC || o.DurationMS < 0 {
			t.Fatalf("%s: invalid observation envelope: %+v", tc.name, o)
		}
		o.Contract, o.SampledAt, o.DurationMS = "", time.Time{}, 0
		x.Target = "http://127.0.0.1:9090"
		got[tc.name] = record{config.Contract(c, x), o}
	}
	path := filepath.Join("testdata", "adapter-compatibility.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]record{}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Error("adapter baseline differs")
		for name, actual := range got {
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(want[name])
			if !bytes.Equal(a, b) {
				t.Errorf("%s changed:\ngot  %s\nwant %s", name, a, b)
			}
		}
	}
	for _, observation := range got {
		raw, _ := json.Marshal(observation)
		if strings.Contains(string(raw), "synthetic-token") || strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "PRESHARED") {
			t.Fatal("secret in compatibility observation")
		}
	}
}
