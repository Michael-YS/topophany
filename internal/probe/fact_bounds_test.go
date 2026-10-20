package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// The busiest HTTP check reads every response label the probe knows: edge
// headers, the Access redirect, the Server-Timing entry and a full trace body,
// through a proxy hop, with a bound address and an exit assertion. Whatever it
// reads, the observation must stay inside the bounds a collector admits, with
// room to spare for the facts the probe adds itself. The server speaks TLS
// because a secret request through a proxy hop is refused over plain HTTP.
func TestHTTPBusiestCheckStaysInsideObservationBounds(t *testing.T) {
	t.Setenv("TOPH_BUSIEST_SECRET", "busiest-secret-3f9a1c")
	cert, key, ca := testkit.Certificate(t, "busiest.test")
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://team.cloudflareaccess.com/cdn-cgi/access/login/x?kid=1")
		w.Header().Set("Cf-Ray", "abc-LAX")
		w.Header().Set("Cf-Cache-Status", "DYNAMIC")
		w.Header().Set("Cf-Mitigated", "challenge")
		w.Header().Set("Server-Timing", `cfL4;desc="?proto=TCP&rtt=1620&min_rtt=1536&retrans=0&lost=0"`)
		w.WriteHeader(http.StatusFound)
		_, _ = io.WriteString(w, traceBody)
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	s.StartTLS()
	defer s.Close()
	for _, tc := range []struct {
		name   string
		secret bool
		// facts that prove the variant read what it should: the plain check
		// keeps the free labels, the secret one only closed words and numbers.
		kept, withheldKeys []string
	}{
		{"plain", false,
			[]string{"edge_pop", "edge_cache", "edge_mitigation", "edge_access", "edge_rtt_ms", "edge_min_rtt_ms", "edge_retrans", "edge_lost", "edge_proto", "trace_colo", "trace_warp", "trace_gateway", "trace_http", "trace_tls", "trace_loc", "exit_ip", "http_status", "bind_ip", "transport_hops"},
			nil},
		{"secret", true,
			[]string{"edge_cache", "edge_mitigation", "edge_access", "edge_rtt_ms", "edge_min_rtt_ms", "edge_retrans", "edge_lost", "edge_proto", "trace_warp", "trace_gateway", "trace_http", "trace_tls", "bind_ip", "transport_hops", "peer_text"},
			[]string{"edge_pop", "trace_colo", "trace_loc", "exit_ip", "http_status"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: socksFixture(t, "", "", nil)}}
			x := httpCheck(s.URL)
			x.Path = "direct"
			x.TimeoutSeconds = 5
			x.CAFile = ca
			x.BindIP = "127.0.0.1"
			x.HTTP.Status = []int{http.StatusFound}
			x.HTTP.ExitFormat = "trace"
			x.HTTP.ExpectedExit = []string{"203.0.113.8"}
			if tc.secret {
				x.HTTP.RequestHeaderEnvs = map[string]string{"X-Secret": "TOPH_BUSIEST_SECRET"}
			}
			o := (Runner{}).Run(context.Background(), c, x)
			if o.Outcome != "ok" || !o.Connectivity {
				t.Fatalf("busiest check: %+v", o)
			}
			for _, key := range tc.kept {
				if _, ok := o.Facts[key]; !ok {
					t.Fatalf("the check did not read %s: %v", key, o.Facts)
				}
			}
			for _, key := range tc.withheldKeys {
				if _, ok := o.Facts[key]; ok {
					t.Fatalf("free label %s survived a secret request: %v", key, o.Facts)
				}
			}
			if code := config.ObservationBounds(o, config.MaxTook); code != "" {
				t.Fatalf("observation refused as %s: %+v", code, o)
			}
			if len(o.Facts) > model.MaxObservationFacts {
				t.Fatalf("%d facts exceed the %d a collector admits: %v", len(o.Facts), model.MaxObservationFacts, o.Facts)
			}
			t.Logf("%d of %d facts", len(o.Facts), model.MaxObservationFacts)
		})
	}
}

// A pool read with the most declared origins at the longest name publishes one
// fact per origin, and each key is "origin." and the name. The longest key must
// still fit the key bound, and the whole observation the fact budget.
func TestRunCloudflarePoolAtDeclaredLimitsStaysInsideObservationBounds(t *testing.T) {
	t.Setenv("TOPH_CLOUDFLARE_PROBE_TOKEN", "test-token")
	var declared []string
	var origins []map[string]map[string]bool
	for i := 0; i < cloudflare.MaxExpectedOrigins; i++ {
		name := fmt.Sprintf("o%02d%s.%s", i, strings.Repeat("x", 60), strings.Repeat("y", 56))
		if len(name) != 120 {
			t.Fatalf("origin %d is %d bytes, want 120", i, len(name))
		}
		declared = append(declared, name)
		origins = append(origins, map[string]map[string]bool{name: {"healthy": true}})
	}
	body, err := json.Marshal(map[string]any{
		"success": true,
		"result":  map[string]any{"pool_id": "44444444444444444444444444444444", "pop_health": map[string]any{"healthy": true, "origins": origins}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	x := cloudflareCheck(srv.URL)
	x.Adapter.Cloudflare = &model.CloudflareOptions{
		Read:            "pool",
		AccountID:       "11111111111111111111111111111111",
		PoolID:          "44444444444444444444444444444444",
		ExpectedOrigins: declared,
	}
	o := (Runner{}).Run(context.Background(), model.Config{Schema: 1, Project: "lab"}, x)
	if o.Outcome != "ok" || o.Facts["origins_healthy"] != "16" {
		t.Fatalf("pool read: %+v", o)
	}
	longest := 0
	for key, value := range o.Facts {
		if strings.HasPrefix(key, "origin.") {
			if value != "healthy" {
				t.Fatalf("%s=%s", key, value)
			}
			longest = max(longest, len(key))
		}
		if len(key) > 128 {
			t.Fatalf("fact key of %d bytes: %.40s", len(key), key)
		}
	}
	if longest != 127 {
		t.Fatalf("the longest declared origin key is %d bytes, want 127", longest)
	}
	if code := config.ObservationBounds(o, config.MaxTook); code != "" {
		t.Fatalf("observation refused as %s: %+v", code, o)
	}
	if len(o.Facts) > model.MaxObservationFacts {
		t.Fatalf("%d facts exceed the %d a collector admits: %v", len(o.Facts), model.MaxObservationFacts, o.Facts)
	}
}
