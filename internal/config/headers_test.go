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
		{"Host": "evil.example/path"}, {"Host": "example.com:"}, {"Host": "例え.jp"},
	} {
		if HTTPHeaders(headers) == nil {
			t.Fatalf("accepted invalid headers %q", headers)
		}
	}
	for _, headers := range []map[string]string{nil, {"X-Key": "a\tb"}, {"X-Key": "日本語"}, {"Host": "example.com."}, {"Host": "[::1]:8080"}} {
		if e := HTTPHeaders(headers); e != nil {
			t.Fatal(e)
		}
	}
}

// A Host must be what net/http will actually send: a reg-name or IP literal
// with an optional in-range port, never a value the transport would reject or
// silently drop.
func TestHostHeaderRule(t *testing.T) {
	for _, v := range []string{
		"example.com", "example.com.", "a-b.example", "host:1", "host:443", "host:65535",
		"xn--fsq.jp", "192.0.2.1", "192.0.2.1:8443",
		"[2001:db8::1]", "[2001:db8::1]:443", "[::1]", "[::1]:8443", "[::ffff:192.0.2.1]",
	} {
		if !HostHeader(v) {
			t.Errorf("refused %q", v)
		}
	}
	for _, v := range []string{
		"", "-host", "example.com:", "example.com:0", "example.com:65536", "example.com:abc",
		"example.com:1:2", "example.com:999999999999999999", "host/path", "host?x", "user@host",
		"<host>", "host>", `host"`, "host name", "host\tname", "host\x7f", "例え.jp",
		"[::1", "::1]", "[::1]extra", "[::1]:", "[::1]:0", "[::1]:65536", "[::1]:-1",
		"[192.0.2.1]", "2001:db8::1", "a[b]", "host]", "[host]", "[::1]]", "[]",
		"host^name", "host|name", "host{name}", "host`name", "host\\name",
	} {
		if HostHeader(v) {
			t.Errorf("accepted %q", v)
		}
	}
}
