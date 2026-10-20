package cloudflared

import (
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestValidateOptions(t *testing.T) {
	intp := func(v int) *int { return &v }
	for _, tc := range []struct {
		name    string
		options model.CloudflaredOptions
		valid   bool
	}{
		{"unset", model.CloudflaredOptions{}, true},
		{"one connection", model.CloudflaredOptions{ExpectedConnections: intp(1)}, true},
		{"eight connections", model.CloudflaredOptions{ExpectedConnections: intp(8)}, true},
		{"zero connections", model.CloudflaredOptions{ExpectedConnections: intp(0)}, false},
		{"nine connections", model.CloudflaredOptions{ExpectedConnections: intp(9)}, false},
		{"negative connections", model.CloudflaredOptions{ExpectedConnections: intp(-1)}, false},
		{"one colo", model.CloudflaredOptions{ExpectedColos: []string{"HKG"}}, true},
		{"lowercase colo", model.CloudflaredOptions{ExpectedColos: []string{"hkg"}}, true},
		{"longest colo", model.CloudflaredOptions{ExpectedColos: []string{"abc12345"}}, true},
		{"eight colos", model.CloudflaredOptions{ExpectedColos: []string{"hkg", "nrt", "lax", "sfo", "sea", "ord", "iad", "jfk"}}, true},
		{"nine colos", model.CloudflaredOptions{ExpectedColos: []string{"hkg", "nrt", "lax", "sfo", "sea", "ord", "iad", "jfk", "sin"}}, false},
		{"two letters", model.CloudflaredOptions{ExpectedColos: []string{"hk"}}, false},
		{"nine letters", model.CloudflaredOptions{ExpectedColos: []string{"abcdefghi"}}, false},
		{"space", model.CloudflaredOptions{ExpectedColos: []string{"H KG"}}, false},
		{"underscore", model.CloudflaredOptions{ExpectedColos: []string{"hk_g"}}, false},
		{"empty", model.CloudflaredOptions{ExpectedColos: []string{""}}, false},
		{"duplicate", model.CloudflaredOptions{ExpectedColos: []string{"HKG", "hkg"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.options); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
}

func TestValidateTarget(t *testing.T) {
	for _, tc := range []struct {
		name, target, ca string
		valid            bool
	}{
		{"ipv4", "http://127.0.0.1:20241", "", true},
		{"ipv4 loopback range", "http://127.0.0.2:20241", "", true},
		{"ipv6", "http://[::1]:20241", "", true},
		{"trailing slash", "http://127.0.0.1:20241/", "", true},
		{"https with ca", "https://127.0.0.1:20241", "/tmp/ca.pem", true},
		{"https without ca", "https://127.0.0.1:20241", "", false},
		{"localhost", "http://localhost:20241", "", false},
		{"remote", "http://192.0.2.1:20241", "", false},
		{"host name", "http://metrics.internal:20241", "", false},
		{"userinfo", "http://user@127.0.0.1:20241", "", false},
		{"query", "http://127.0.0.1:20241/?x=1", "", false},
		{"fragment", "http://127.0.0.1:20241/#x", "", false},
		{"path", "http://127.0.0.1:20241/metrics", "", false},
		{"no port", "http://127.0.0.1", "", false},
		{"zero port", "http://127.0.0.1:0", "", false},
		{"bad port", "http://127.0.0.1:99999", "", false},
		{"scheme", "ftp://127.0.0.1:20241", "", false},
		{"opaque", "http:127.0.0.1:20241", "", false},
		{"control", "http://127.0.0.1:20241/\x1b", "", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTarget(tc.target, tc.ca); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
}

func TestValidateTargetNamesTheReason(t *testing.T) {
	if err := ValidateTarget("http://localhost:20241", ""); err == nil || !strings.Contains(err.Error(), "literal loopback") {
		t.Fatalf("localhost error is %v", err)
	}
	if err := ValidateTarget("http://127.0.0.1:20241/metrics", ""); err == nil || !strings.Contains(err.Error(), "no path") {
		t.Fatalf("path error is %v", err)
	}
}
