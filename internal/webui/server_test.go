//go:build webui

package webui

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestWebHostOriginAndReadOnlySurface(t *testing.T) {
	called := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Origin") != "" {
			t.Fatal("collector CORS leaked")
		}
		w.WriteHeader(204)
	})
	h := Handler(backend, "127.0.0.1:8080", "http", "")
	for _, tc := range []struct {
		method, path, host, origin string
		want                       int
		called                     bool
	}{
		{"GET", "/", "127.0.0.1:8080", "", 200, false},
		{"GET", "/app.js", "localhost:8080", "", 200, false},
		{"GET", "/", "evil.test:8080", "", 403, false},
		{"GET", "/v1/web/view", "127.0.0.1:8080", "https://evil.test", 403, false},
		{"GET", "/v1/web/view", "127.0.0.1:8080", "http://127.0.0.1:8080", 204, true},
		{"POST", "/v1/observations", "127.0.0.1:8080", "", 404, false},
		{"GET", "/v1/worker", "127.0.0.1:8080", "", 404, false},
		{"PUT", "/v1/config", "127.0.0.1:8080", "", 404, false},
		{"GET", "/assets/", "127.0.0.1:8080", "", 404, false},
	} {
		t.Run(tc.path+tc.host+tc.origin, func(t *testing.T) {
			called = false
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || called != tc.called {
				t.Fatal(w.Code, called, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("missing security headers")
			}
		})
	}
}

func TestWebProxyHostOrigin(t *testing.T) {
	const external = "https://pi.example.ts.net"
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			t.Error("collector CORS leaked")
		}
		w.WriteHeader(200)
	})
	for _, tc := range []struct {
		name, proxy, host, origin, peer, site string
		forwarded                             bool
		want                                  int
	}{
		{"default local", "", "localhost:8080", "http://localhost:8080", "127.0.0.1:1234", "same-origin", false, 200},
		{"default external", "", "pi.example.ts.net", external, "127.0.0.1:1234", "same-origin", false, 403},
		{"default forged headers", "", "evil.test", "https://evil.test", "127.0.0.1:1234", "", true, 403},
		{"preserved host", external, "pi.example.ts.net", external, "127.0.0.1:1234", "same-origin", false, 200},
		{"rewritten host", external, "127.0.0.1:8080", external, "127.0.0.1:1234", "same-origin", false, 200},
		{"localhost alias", external, "localhost:8080", external, "127.0.0.1:1234", "same-origin", false, 200},
		{"IPv6 peer", external, "pi.example.ts.net", external, "[::1]:1234", "same-origin", false, 200},
		{"navigation without origin", external, "pi.example.ts.net", "", "127.0.0.1:1234", "none", false, 200},
		{"direct still works", external, "localhost:8080", "http://localhost:8080", "127.0.0.1:1234", "same-origin", false, 200},
		{"wrong host", external, "evil.test", external, "127.0.0.1:1234", "", false, 403},
		{"wrong origin", external, "pi.example.ts.net", "https://evil.test", "127.0.0.1:1234", "", false, 403},
		{"rewritten wrong origin", external, "127.0.0.1:8080", "https://evil.test", "127.0.0.1:1234", "", false, 403},
		{"origin must match exactly", external, "pi.example.ts.net", external + ":443", "127.0.0.1:1234", "", false, 403},
		{"external host local origin", external, "pi.example.ts.net", "http://pi.example.ts.net", "127.0.0.1:1234", "", false, 403},
		{"cross site", external, "pi.example.ts.net", external, "127.0.0.1:1234", "cross-site", false, 403},
		{"rewritten cross site", external, "127.0.0.1:8080", external, "127.0.0.1:1234", "cross-site", false, 403},
		{"forged headers", external, "evil.test", "https://evil.test", "127.0.0.1:1234", "", true, 403},
		{"remote peer", external, "pi.example.ts.net", external, "192.0.2.1:1234", "", true, 403},
		{"remote rewritten host", external, "127.0.0.1:8080", external, "192.0.2.1:1234", "", true, 403},
		{"remote navigation", external, "pi.example.ts.net", "", "192.0.2.1:1234", "", false, 403},
		{"missing peer", external, "pi.example.ts.net", external, "", "", false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Handler(backend, "127.0.0.1:8080", "http", tc.proxy)
			for _, path := range []string{"/", "/app.js", "/v1/web/view"} {
				r := httptest.NewRequest("GET", "http://"+tc.host+path, nil)
				r.RemoteAddr = tc.peer
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Sec-Fetch-Site", tc.site)
				if tc.forwarded {
					r.Header.Set("X-Forwarded-Host", "pi.example.ts.net")
					r.Header.Set("X-Forwarded-Proto", "https")
					r.Header.Set("X-Forwarded-For", "127.0.0.1")
					r.Header.Set("Forwarded", "for=127.0.0.1;host=pi.example.ts.net;proto=https")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != tc.want {
					t.Fatalf("%s: got %d, want %d: %s", path, w.Code, tc.want, w.Body.String())
				}
				if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Content-Security-Policy") == "" {
					t.Fatal("missing security headers")
				}
			}
		})
	}
}

func TestWebProxyConfiguration(t *testing.T) {
	for _, origin := range []string{
		"http://pi.example.ts.net", "https://*.ts.net", "https://pi.example.ts.net/",
		"https://pi.example.ts.net/path", "https://pi.example.ts.net?x=1", "https://pi.example.ts.net?",
		"https://pi.example.ts.net#fragment", "https://pi.example.ts.net#", "https://user@pi.example.ts.net",
		"https://pi.example.ts.net https://other.test", "https://", "null", "https://bad_host",
		"https://.example", "https://-bad.example", "https://pi.example.ts.net:0", "https://pi.example.ts.net:65536",
		"https://pi.example.ts.net:", "https://[bad]", "https://::1", "https://pi.example.ts.net:abc",
	} {
		t.Run(origin, func(t *testing.T) {
			stop, _, err := Start("127.0.0.1:0", origin, http.NotFoundHandler(), nil, make(chan error, 1))
			if err == nil {
				stop()
				t.Fatal("invalid proxy origin accepted")
			}
		})
	}
	for _, origin := range []string{"", "https://pi.example.ts.net", "https://pi.example.ts.net:8443", "https://127.0.0.1", "https://[::1]:8443"} {
		if err := ValidateProxyOrigin(origin); err != nil {
			t.Fatalf("valid origin %q rejected: %v", origin, err)
		}
	}
	// Even TLS must not permit proxy mode on a non-loopback listener.
	for _, addr := range []string{"0.0.0.0:0", "192.0.2.1:0", "[::]:0", ":0"} {
		stop, _, err := Start(addr, "https://pi.example.ts.net", http.NotFoundHandler(), &tls.Config{}, make(chan error, 1))
		if err == nil {
			stop()
			t.Fatal("proxy enabled on non-loopback listener", addr)
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Fatal("expected loopback validation before binding", err)
		}
	}
}

func TestWebProxyAuthentication(t *testing.T) {
	c := testkit.Config()
	s, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	viewer, worker := strings.Repeat("a", 64), strings.Repeat("b", 64)
	auth := api.Auth{Schema: 1, Tokens: []api.Principal{
		{TokenSHA256: secure.Hash(viewer), Role: "viewer"},
		{TokenSHA256: secure.Hash(worker), Role: "worker", Observer: "home"},
	}}
	backend := api.New(c, "test", s, auth)
	backend.WebEnabled = true
	// The collector's separate CORS setting must not affect native proxy access.
	backend.WebOrigin = "https://collector.example"
	h := Handler(backend.Handler(), "127.0.0.1:8080", "http", "https://pi.example.ts.net")
	for _, tc := range []struct {
		token string
		want  int
	}{{viewer, 200}, {worker, 403}, {"", 401}, {"invalid", 401}} {
		for _, path := range []string{"/v1/web/view", "/v1/web/events"} {
			ctx, cancel := context.WithCancel(context.Background())
			// Cancel only the streaming lifetime; the initial frame still exercises
			// real authentication and projection without waiting for stream expiry.
			cancel()
			r := httptest.NewRequest("GET", "http://pi.example.ts.net"+path, nil).WithContext(ctx)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Origin", "https://pi.example.ts.net")
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%s: got %d, want %d: %s", path, w.Code, tc.want, w.Body.String())
			}
			if path == "/v1/web/events" && tc.want == 200 && !strings.Contains(w.Body.String(), "event: view\n") {
				t.Fatal("missing initial SSE frame")
			}
		}
	}
}
func TestWebListenerValidationAndLifecycle(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", "192.0.2.1:8080", "example.com:8080", ":8080", "127.0.0.1:no"} {
		if stop, _, err := Start(addr, "", http.NotFoundHandler(), nil, make(chan error, 1)); err == nil {
			stop()
			t.Fatal("unsafe listener accepted", addr)
		}
	}
	stop, addr, err := Start("127.0.0.1:0", "", http.NotFoundHandler(), nil, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Topophany") {
		t.Fatal(resp.StatusCode)
	}
	stop()
	stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
	if resp, err = client.Do(r); err == nil {
		resp.Body.Close()
		t.Fatal("listener survived stop")
	}
}
