package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestInvalidAndInsecureHeadersNeverDial(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for _, tt := range []struct {
		headers      map[string]string
		target, code string
	}{
		{map[string]string{"Bad Header": "private"}, srv.URL, "invalid_http_headers"},
		{map[string]string{"X-Key": "private\x00value"}, srv.URL, "invalid_http_headers"},
		{map[string]string{"X-API-Key": "private"}, "http://example.invalid/", "insecure_http_credentials"},
	} {
		c := testkit.Config()
		x := &c.Checks[0]
		x.Target = tt.target
		x.ResolveIP = "127.0.0.1"
		x.HTTP.RequestHeaders = tt.headers
		if config.Validate(c) == nil {
			t.Fatal("invalid configuration accepted")
		}
		o := (Runner{}).Run(context.Background(), c, *x)
		if o.Outcome != "error" || o.Stage != "setup" || o.Code != tt.code {
			t.Fatalf("%+v", o)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("invalid check dialled the server")
	}
}

func TestProtectedPrivateHeadersStillWork(t *testing.T) {
	var hits atomic.Int32
	srv, ca := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "private" {
			hits.Add(1)
		}
		w.Write([]byte("ok"))
	}))
	x := httpCheck(srv.URL)
	x.CAFile = ca
	x.HTTP.RequestHeaders = map[string]string{"X-API-Key": "private"}
	o := (Runner{}).Run(context.Background(), model.Config{}, x)
	if o.Outcome != "ok" || hits.Load() != 1 {
		t.Fatalf("%+v hits=%d", o, hits.Load())
	}
}
