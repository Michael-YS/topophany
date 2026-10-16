package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// When no collector answers, the error says why each did not, as a first
// connection most often fails on one of these.
func TestNoAnswerSaysWhy(t *testing.T) {
	token := strings.Repeat("a", 64)
	closed, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	refused := "https://" + closed.Addr().String()
	closed.Close()
	untrusted := httptest.NewTLSServer(http.NotFoundHandler())
	defer untrusted.Close()
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	for _, c := range []struct{ url, want string }{
		{refused, "connection refused"},
		{untrusted.URL, "authority not trusted"},
		{strings.Replace(plain.URL, "http://", "https://", 1), "not answering in TLS"},
	} {
		cl, e := New(Profile{Project: "demo", Servers: []Endpoint{{URL: c.url, Token: token}}})
		if e != nil {
			t.Fatal(e)
		}
		_, _, e = cl.Snapshot(context.Background(), "")
		cl.Close()
		host := strings.TrimPrefix(strings.TrimPrefix(c.url, "https://"), "http://")
		if !errors.Is(e, ErrNoCollector) || !strings.Contains(e.Error(), host+": ") || !strings.Contains(e.Error(), c.want) {
			t.Fatalf("%s: %v", c.want, e)
		}
	}
}
