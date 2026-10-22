package integration

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/source"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// This exercises the real production source transport, rather than a supplied
// source.IO or http.Client. Every network endpoint is a loopback fixture.
func TestBeszelProductionTLSAndExplicitRouteProtectCredentials(t *testing.T) {
	t.Setenv("TOPO_BESZEL_INTEGRATION_TOKEN", beszelToken)
	certFile, keyFile, caFile := testkit.Certificate(t, "beszel.example.test")
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var hubReads, ambientReads, routeReads atomic.Int64
	var expectedHost string
	hub := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubReads.Add(1)
		if r.TLS == nil || r.TLS.ServerName != "beszel.example.test" || r.Host != expectedHost {
			t.Error("pinned dialing did not preserve the configured TLS name and HTTP Host")
		}
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != beszelToken || r.URL.Path != beszelPrefix+"/api/collections/system_stats/records" {
			t.Error("unexpected credentialed request reached the Hub fixture")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[]}`)
	}))
	_, port, err := net.SplitHostPort(hub.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	expectedHost = net.JoinHostPort("beszel.example.test", port)
	hub.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	hub.Config.ErrorLog = log.New(io.Discard, "", 0)
	hub.StartTLS()
	t.Cleanup(hub.Close)
	ambient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ambientReads.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(ambient.Close)
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, ambient.URL)
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	c := beszelIntegrationConfig(t, "https://"+expectedHost)
	c.Sources[0].ResolveIP = "127.0.0.1"
	c.Sources[0].CAFile = caFile
	rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	collect := func(t *testing.T, cfg model.Config) error {
		t.Helper()
		if err := config.Validate(cfg); err != nil {
			t.Fatal(err)
		}
		if err := rt.Validate(cfg); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, err := rt.Collect(ctx, cfg, cfg.Sources[0], []source.Request{{Object: beszelSystemID, Reports: []string{model.ReportTypeResource}}})
		if len(got) != 0 {
			t.Fatal("empty fixture list produced a resource report")
		}
		if err != nil && strings.Contains(err.Error(), beszelToken) {
			t.Fatal("transport refusal reflected the source credential")
		}
		return err
	}
	t.Run("trusted-pinned-origin-ignores-ambient-proxy", func(t *testing.T) {
		if err := collect(t, c); err != nil {
			t.Fatal(err)
		}
		if hubReads.Load() != 1 || ambientReads.Load() != 0 {
			t.Fatalf("wrong route: Hub reads=%d ambient proxy reads=%d", hubReads.Load(), ambientReads.Load())
		}
	})
	for _, mode := range []string{"untrusted-ca", "wrong-server-name"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.Clone(c)
			if mode == "untrusted-ca" {
				cfg.Sources[0].CAFile = ""
			} else {
				cfg.Sources[0].ServerName = "wrong.example.test"
			}
			before := hubReads.Load()
			if err := collect(t, cfg); err == nil || source.Code(err) != "transport_error" {
				t.Fatalf("unverified TLS succeeded or returned the wrong failure class: %v", err)
			}
			if hubReads.Load() != before {
				t.Fatal("authorization reached the HTTP handler before certificate validation")
			}
		})
	}
	t.Run("failed-explicit-proxy-never-falls-back", func(t *testing.T) {
		route := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			routeReads.Add(1)
			if r.Method != http.MethodConnect || r.Header.Get("Authorization") != "" {
				t.Error("source authorization was sent outside the TLS tunnel")
			}
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer route.Close()
		cfg := config.Clone(c)
		cfg.Paths = append(cfg.Paths, model.Path{ID: "beszel-denied", Observer: "cloud", Nodes: []string{"aliyun", "dmit"}, Proxies: []model.Proxy{{Kind: "http-connect", Address: strings.TrimPrefix(route.URL, "http://")}}})
		cfg.Sources[0].Path = "beszel-denied"
		before := hubReads.Load()
		if err := collect(t, cfg); err == nil || source.Code(err) != "transport_error" {
			t.Fatalf("failed explicit route succeeded or changed failure class: %v", err)
		}
		if routeReads.Load() != 1 || hubReads.Load() != before || ambientReads.Load() != 0 {
			t.Fatalf("failed route fell back: route=%d Hub=%d ambient=%d", routeReads.Load(), hubReads.Load(), ambientReads.Load())
		}
	})
}
