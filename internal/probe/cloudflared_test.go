package probe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflared"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// The cloudflared adapter reads a local process. Its dialer accepts only a
// literal loopback address and has no direct or proxied fallback.
func TestCloudflaredDialIsLoopbackOnly(t *testing.T) {
	if _, err := loopbackDial(context.Background(), "tcp", "192.0.2.1:20241"); err == nil {
		t.Fatal("remote address accepted")
	}
	if _, err := loopbackDial(context.Background(), "tcp", "metrics.internal:20241"); err == nil {
		t.Fatal("host name accepted")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
			close(accepted)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := loopbackDial(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	select {
	case <-accepted:
	case <-ctx.Done():
		t.Fatal("loopback listener was not reached")
	}
}

// The adapter receives a client with no proxy at all, so neither the check's
// path nor an ambient HTTP_PROXY can route a loopback read through another
// host. The dialer refuses a non-loopback destination.
func TestCloudflaredClientRefusesProxies(t *testing.T) {
	for _, tc := range []struct{ name, target string }{
		{"http", "http://127.0.0.1:20241"},
		{"https", "https://127.0.0.1:20241"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, closeClient, err := cloudflaredClient(model.Check{Kind: "cloudflared", ServerName: "listener.test"}, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			tr, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("transport %T", client.Transport)
			}
			if tr.Proxy != nil || !tr.DisableKeepAlives || !tr.DisableCompression || tr.MaxResponseHeaderBytes != 32768 {
				t.Fatalf("unbounded or proxied transport: %+v", tr)
			}
			if tr.TLSClientConfig == nil || tr.TLSClientConfig.ServerName != "listener.test" {
				t.Fatalf("TLS name not from server_name: %+v", tr.TLSClientConfig)
			}
			if _, err := tr.DialContext(context.Background(), "tcp", "192.0.2.1:20241"); err == nil {
				t.Fatal("non-loopback dial accepted")
			}
		})
	}
}

// Run must classify a listener that answers in the wrong shape, or not at
// all, as a local monitor error: reported evidence, adapter stage, no
// connectivity, and no target outage.
func TestRunCloudflaredMetricsUnavailable(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddress := closed.Addr().String()
	closed.Close()
	for _, tc := range []struct {
		name  string
		ready func(http.ResponseWriter)
		path  string
	}{
		{"ready 500", func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }, ""},
		{"ready 404", func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }, ""},
		{"ready html", func(w http.ResponseWriter) { io.WriteString(w, "<html>busy</html>") }, ""},
		{"established but metrics 404", func(w http.ResponseWriter) {
			io.WriteString(w, `{"status":200,"readyConnections":4}`)
		}, "/metrics-404"},
		{"closed listener", nil, "http://" + closedAddress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.path
			if tc.ready != nil {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.URL.Path == "/metrics":
						w.WriteHeader(http.StatusNotFound)
					default:
						tc.ready(w)
					}
				}))
				defer s.Close()
				target = s.URL
			}
			c := model.Config{Schema: 1, Project: "lab"}
			x := model.Check{ID: "tunnel", Name: "cloudflared", Observer: "home", Kind: "cloudflared", Target: target, TimeoutSeconds: 5}
			o := Runner{}.Run(context.Background(), c, x)
			if o.Outcome != "error" || o.Stage != "adapter" || o.Code != "metrics_unavailable" {
				t.Fatalf("got %+v", o)
			}
			if o.Evidence != "reported" || o.PathEvidence != cloudflared.PathEvidence || o.Connectivity {
				t.Fatalf("envelope %+v", o)
			}
			if _, ok := o.Facts["connection_semantics"]; ok {
				t.Fatal("cloudflared keeps the request-stream fact")
			}
		})
	}
}
