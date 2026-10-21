package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestSourceIOStaysOnOriginBoundsBodiesAndDoesNotRedirect(t *testing.T) {
	var next int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("hello"))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 32)))
		case "/redirect":
			http.Redirect(w, r, "/next", http.StatusFound)
		case "/next":
			atomic.AddInt32(&next, 1)
			_, _ = w.Write([]byte("followed"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	src := model.Source{ID: "source", Adapter: "synthetic-list", Observer: "home", Target: srv.URL + "/api", CredentialEnv: "TOPO_SOURCE_TOKEN", MaxResponseBytes: 16, MaxPages: 2}
	t.Setenv("TOPO_SOURCE_TOKEN", "synthetic-token")
	c := model.Config{}
	sio, err := newSourceIO(c, src)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred, err := sio.Credential(ctx, src)
	if err != nil || cred != "synthetic-token" {
		t.Fatalf("credential = %q, %v", cred, err)
	}
	resp, err := sio.Get(ctx, src, "/data", map[string]string{"Authorization": "Bearer token"}, 5)
	if err != nil || resp.Status != 200 || string(resp.Body) != "hello" || resp.ContentType != "application/json" {
		t.Fatalf("response = %+v, %v", resp, err)
	}
	if _, err := sio.Get(ctx, src, "/big", nil, 5); err == nil {
		t.Fatal("oversized body accepted")
	}
	if _, err := sio.Get(ctx, src, "http://example.invalid/data", nil, 5); err == nil {
		t.Fatal("cross-origin page accepted")
	}
	if _, err := sio.Get(ctx, src, "/data", map[string]string{"X-Test": "a\r\nInjected: x"}, 5); err == nil {
		t.Fatal("header injection accepted")
	}
	if _, err := sio.Get(ctx, src, "/data", map[string]string{"Host": "other.invalid"}, 5); err == nil {
		t.Fatal("transport-owned header accepted")
	}
	redirectSource := src
	redirectSource.MaxResponseBytes = 1024
	sioRedirect, err := newSourceIO(c, redirectSource)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = sioRedirect.Get(ctx, redirectSource, "/redirect", nil, 1024)
	if err != nil || resp.Status != http.StatusFound || atomic.LoadInt32(&next) != 0 {
		t.Fatalf("redirect = %+v, next=%d, err=%v", resp, atomic.LoadInt32(&next), err)
	}
	// Two successful/page-budget calls were already spent above; a fresh IO
	// proves the page limit itself.
	sio2, err := newSourceIO(c, src)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := sio2.Get(ctx, src, "/data", nil, 16); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sio2.Get(ctx, src, "/data", nil, 16); err == nil {
		t.Fatal("page budget not enforced")
	}
}

func TestSourceIOCredentialMissingAndInvalidTarget(t *testing.T) {
	src := model.Source{ID: "source", Adapter: "synthetic-list", Observer: "home", Target: "https://probe.invalid/api", CredentialEnv: "TOPO_MISSING_SOURCE_TOKEN", MaxResponseBytes: 1024, MaxPages: 1}
	t.Setenv("TOPO_MISSING_SOURCE_TOKEN", "")
	sio, err := newSourceIO(model.Config{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sio.Credential(context.Background(), src); err == nil {
		t.Fatal("missing credential accepted")
	}
	if _, err := newSourceIO(model.Config{}, model.Source{ID: "bad", Adapter: "synthetic-list", Observer: "home", Target: "ftp://probe.invalid/file"}); err == nil {
		t.Fatal("non-HTTP source target accepted")
	}
}
