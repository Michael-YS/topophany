package cloudflared

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// MaxConnections bounds expected_connections. A connector's HA pool is small
// and the roadmap declares 1..8; a larger number is more likely a typo than
// a deployment.
const MaxConnections = 8

// MaxColos bounds expected_colos, matching the eight tokens one report may
// carry.
const MaxColos = 8

// A colo token is the lowercase shape cloudflared labels edge_location with:
// three to eight ASCII letters or digits.
var coloToken = regexp.MustCompile(`^[0-9a-z]{3,8}$`)

// Validate checks the nested options. Expected colos are compared without
// case because Cloudflare names a colo uppercase in its headers and
// documentation while the connector's edge_location label is lowercase; the
// report keeps the lowercase form and a declared entry may use either.
func Validate(options model.CloudflaredOptions) error {
	if value := options.ExpectedConnections; value != nil && (*value < 1 || *value > MaxConnections) {
		return errors.New("cloudflared expected_connections must be 1..8")
	}
	if len(options.ExpectedColos) > MaxColos {
		return errors.New("cloudflared expected_colos accepts at most 8 colos")
	}
	seen := make(map[string]bool, len(options.ExpectedColos))
	for _, colo := range options.ExpectedColos {
		token := strings.ToLower(colo)
		if !coloToken.MatchString(token) || seen[token] {
			return errors.New("cloudflared expected_colos must be 3..8 letters or digits, without duplicates")
		}
		seen[token] = true
	}
	return nil
}

// ValidateTarget accepts only the connector's own listener: a literal
// loopback address with an explicit port, over http, or https with a CA the
// connector's listener certifies itself with. A host name is refused because
// a hosts file or resolver could point it elsewhere, and `localhost` is one.
// The URL carries no user information, query, fragment or path other than a
// bare `/`; the adapter appends its two fixed reads to it.
func ValidateTarget(target, caFile string) error {
	u, err := url.Parse(target)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || secure.Text(target) != target {
		return errors.New("cloudflared target must be a literal loopback http(s) URL without userinfo, query or fragment")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("cloudflared target must use http or https")
	}
	if path := u.Path; path != "" && path != "/" {
		return errors.New("cloudflared reads only /ready and /metrics; the target carries no path")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("cloudflared target must be a literal loopback address (127.0.0.0/8 or ::1)")
	}
	port, err := strconv.Atoi(u.Port())
	if u.Port() == "" || err != nil || port < 1 || port > 65535 {
		return errors.New("cloudflared target needs an explicit 1..65535 port")
	}
	if u.Scheme == "https" && caFile == "" {
		return errors.New("cloudflared https target requires ca_file")
	}
	return nil
}
