package config

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestPrivateHeadersRequireProtectedTransport(t *testing.T) {
	for _, tt := range []struct {
		target, resolve string
		proxy, allowed  bool
	}{
		{"http://example.invalid/", "", false, false},
		{"http://example.invalid/", "127.0.0.1", false, false},
		{"http://127.0.0.1/", "192.0.2.1", false, false},
		{"http://127.0.0.1/", "", true, false},
		{"http://127.0.0.1/", "", false, true},
		{"https://example.invalid/", "", true, true},
	} {
		for _, header := range []string{"X-API-Key", "Proxy-Authorization", "Host"} {
			x := model.Check{Kind: "http", Target: tt.target, ResolveIP: tt.resolve, HTTP: model.HTTPOptions{RequestHeaders: map[string]string{header: "private"}}}
			c := model.Config{}
			if tt.proxy {
				x.Path = "p"
				c.Paths = []model.Path{{ID: "p", Proxies: []model.Proxy{{Kind: "http-connect", Address: "127.0.0.1:8080"}}}}
			}
			if e := HTTPCredentials(c, x); (e == nil) != tt.allowed {
				t.Fatalf("%+v %s: %v", tt, header, e)
			}
		}
	}
}

func TestHeaderWireSyntax(t *testing.T) {
	for _, headers := range []map[string]string{
		{"Bad Header": "x"}, {"": "x"}, {"X:Key": "x"}, {"X-密钥": "x"},
		{"X-Key": "a\x00b"}, {"X-Key": "a\x7fb"}, {"X-Key": "a\r\nb"},
		{"X-Key": "a", "x-key": "b"}, {"Cookie": "x"}, {"Authorization": "x"},
		{"Host": "evil.example/path"},
	} {
		if HTTPHeaders(headers) == nil {
			t.Fatalf("accepted invalid headers %q", headers)
		}
	}
	for _, headers := range []map[string]string{nil, {"X-Key": "a\tb"}, {"X-Key": "日本語"}, {"Host": "[::1]:8080"}} {
		if e := HTTPHeaders(headers); e != nil {
			t.Fatal(e)
		}
	}
}
