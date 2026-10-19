package cloudflare

import (
	"net/url"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// AccessFacts reads Cloudflare Access's answer to a request without an Access
// session without recording where it points: the login redirect carries a key
// ID and the address the user asked for, so only the closed word that Access
// asked for a login is kept.
//
// Cloudflare documents a browser request without an Access session as a 302
// to
// https://<team>.cloudflareaccess.com/cdn-cgi/access/login/<host>?kid=...&redirect_url=...
// (Debugging Pages, "Blocked HTTP validation"); a failed service-token
// authentication returns 401 or 403 instead. 303 and 307 are accepted as
// redirects too; no other status is. The location must be an absolute URL
// without user information, and its Hostname, lowercased, must end in
// ".cloudflareaccess.com" with at least one character before the suffix. The
// scheme is not restricted, and Hostname strips the port, even an out-of-range
// one, so ".team.cloudflareaccess.com" and "..cloudflareaccess.com" pass too:
// this follows the documented host suffix, not the team-name syntax.
// Malformed or opaque URLs, relative locations and locations with control or
// bidi characters are refused, and a lookalike such as
// cloudflareaccess.com.evil does not end in the dotted suffix.
func AccessFacts(status int, location string) map[string]string {
	switch status {
	case 302, 303, 307:
	default:
		return nil
	}
	if secure.Text(location) != location {
		return nil
	}
	u, err := url.Parse(location)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.Opaque != "" {
		return nil
	}
	const domain = ".cloudflareaccess.com"
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, domain) || len(host) == len(domain) {
		return nil
	}
	return map[string]string{"edge_access": "login_redirect"}
}
