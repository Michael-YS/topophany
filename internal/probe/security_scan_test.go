package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestUnsafeCredentialTransportFailsBeforeDial(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	t.Setenv("SECRET", "test-secret")
	c := testkit.Config()
	x := httpCheck(server.URL)
	x.HTTP.BearerEnv = "SECRET"
	x.ResolveIP = "203.0.113.1"
	if v := (Runner{}).Run(context.Background(), c, x); v.Code != "insecure_http_credentials" {
		t.Fatal(v)
	}
	x.ResolveIP = ""
	x.Path = "direct"
	c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: "127.0.0.1:1"}}
	if v := (Runner{}).Run(context.Background(), c, x); v.Code != "insecure_http_credentials" {
		t.Fatal(v)
	}
	// A query is sent as a credential is, whether or not a token is named.
	x = httpCheck(server.URL + "/?token=test-secret")
	x.ResolveIP = "203.0.113.1"
	if v := (Runner{}).Run(context.Background(), testkit.Config(), x); v.Code != "insecure_http_credentials" {
		t.Fatal(v)
	}
	d := Dialer{Proxies: []model.Proxy{{Kind: "socks5", Address: "203.0.113.1:1080", PasswordEnv: "SECRET"}}}
	if _, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("unsafe proxy attempted connection")
	}
	if hits.Load() != 0 {
		t.Fatal("unsafe credentials reached HTTP target")
	}
}
