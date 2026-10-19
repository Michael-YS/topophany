package cloudflare_test

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// A browser request without an Access session is redirected to the team's
// cloudflareaccess.com login, whose URL carries a key ID and the address
// asked for. Only the closed fact that Access asked for a login is kept, and
// only from a redirect to that domain: a lookalike host, a relative location
// or a location with control characters is not evidence.
func TestAccessFacts(t *testing.T) {
	const login = "https://team.cloudflareaccess.com/cdn-cgi/access/login/host?kid=x&redirect_url=y"
	for _, tc := range []struct {
		name     string
		status   int
		location string
		want     map[string]string
	}{
		{"302 login redirect", 302, login, map[string]string{"edge_access": "login_redirect"}},
		{"303 login redirect", 303, login, map[string]string{"edge_access": "login_redirect"}},
		{"307 login redirect", 307, login, map[string]string{"edge_access": "login_redirect"}},
		{"200 with the location", 200, login, nil},
		{"301 with the location", 301, login, nil},
		{"308 with the location", 308, login, nil},
		{"matching host", 302, "https://team.cloudflareaccess.com/", map[string]string{"edge_access": "login_redirect"}},
		{"exact root host", 302, "https://cloudflareaccess.com/cdn-cgi/access/login/host", nil},
		{"attacker suffix", 302, "https://cloudflareaccess.com.evil/cdn-cgi/access/login/host", nil},
		{"attacker prefix", 302, "https://evilcloudflareaccess.com/cdn-cgi/access/login/host", nil},
		{"userinfo trick", 302, "https://team.cloudflareaccess.com@evil.example/cdn-cgi/access/login/host", nil},
		{"userinfo before a matching host", 302, "https://evil.example@team.cloudflareaccess.com/cdn-cgi/access/login/host", nil},
		{"port", 302, "https://team.cloudflareaccess.com:8443/cdn-cgi/access/login/host", map[string]string{"edge_access": "login_redirect"}},
		{"relative location", 302, "/cdn-cgi/access/login/host?kid=x&redirect_url=y", nil},
		{"network-path reference", 302, "//team.cloudflareaccess.com/cdn-cgi/access/login/host", nil},
		{"mixed case", 302, "HTTPS://Team.CloudflareAccess.COM/cdn-cgi/access/login/host", map[string]string{"edge_access": "login_redirect"}},
		{"dot label prefix", 302, "https://.team.cloudflareaccess.com/cdn-cgi/access/login/host", map[string]string{"edge_access": "login_redirect"}},
		{"leading-dot prefix", 302, "https://..cloudflareaccess.com/cdn-cgi/access/login/host", map[string]string{"edge_access": "login_redirect"}},
		{"non-https scheme", 302, "http://team.cloudflareaccess.com/cdn-cgi/access/login/host", map[string]string{"edge_access": "login_redirect"}},
		{"out-of-range port", 302, "https://team.cloudflareaccess.com:70000/cdn-cgi/access/login/host", map[string]string{"edge_access": "login_redirect"}},
		{"trailing dot", 302, "https://team.cloudflareaccess.com./cdn-cgi/access/login/host", nil},
		{"empty team", 302, "https://.cloudflareaccess.com/cdn-cgi/access/login/host", nil},
		{"no host", 302, "https:///cdn-cgi/access/login/host", nil},
		{"malformed", 302, "https://[::1", nil},
		{"control characters", 302, "https://team.cloudflareaccess.com/\x1b[2J", nil},
		{"bidi control", 302, "https://team.cloudflareaccess.com/\u202e", nil},
		{"empty location", 302, "", nil},
		{"whitespace only", 302, " \t ", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cloudflare.AccessFacts(tc.status, tc.location)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("AccessFacts(%d, %q) = %#v; want %#v", tc.status, tc.location, got, tc.want)
			}
		})
	}
}

// FuzzAccessFacts keeps the parser total on arbitrary locations and pins
// every accepted location to the narrow shape the parser documents, so
// removing a guard makes the target fail rather than silently pass.
func FuzzAccessFacts(f *testing.F) {
	f.Add(302, "https://team.cloudflareaccess.com/cdn-cgi/access/login/host?kid=x&redirect_url=y")
	f.Add(307, "https://cloudflareaccess.com.evil/cdn-cgi/access/login/host")
	f.Add(302, "/cdn-cgi/access/login/host")
	f.Add(302, "https://.team.cloudflareaccess.com/cdn-cgi/access/login/host")
	f.Add(302, "https://..cloudflareaccess.com/cdn-cgi/access/login/host")
	f.Add(302, "http://team.cloudflareaccess.com/cdn-cgi/access/login/host")
	f.Add(302, "https://team.cloudflareaccess.com:70000/cdn-cgi/access/login/host")
	f.Add(200, "")
	f.Fuzz(func(t *testing.T, status int, location string) {
		facts := cloudflare.AccessFacts(status, location)
		if facts == nil {
			return
		}
		if status != 302 && status != 303 && status != 307 {
			t.Fatalf("status %d produced %#v", status, facts)
		}
		if len(facts) != 1 || facts["edge_access"] != "login_redirect" {
			t.Fatalf("unexpected facts %#v", facts)
		}
		if secure.Text(location) != location {
			t.Fatalf("accepted a location with control characters: %q", location)
		}
		u, err := url.Parse(location)
		if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.Opaque != "" {
			t.Fatalf("accepted a location the checks reject: %q (%v)", location, err)
		}
		const domain = ".cloudflareaccess.com"
		host := strings.ToLower(u.Hostname())
		if !strings.HasSuffix(host, domain) || len(host) == len(domain) {
			t.Fatalf("accepted a host %q outside the suffix rule", host)
		}
	})
}
