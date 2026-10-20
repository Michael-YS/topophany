package config

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

var errForbiddenHeader = errors.New("use bearer_env; Authorization/Cookie forbidden")

// HTTPHeaders checks the wire syntax before a check can run. Values may
// contain HTAB and non-ASCII bytes, but no other controls. Names are HTTP
// tokens. Errors deliberately contain neither header names nor values.
func HTTPHeaders(headers map[string]string) error {
	named := make(map[string]bool, len(headers))
	for k, v := range headers {
		if !httpToken(k) {
			return errors.New("invalid HTTP header name")
		}
		if !HTTPHeaderValue(v) {
			return errors.New("invalid HTTP header value")
		}
		name := strings.ToLower(k)
		if named[name] {
			return errors.New("duplicate HTTP header name")
		}
		named[name] = true
		if name == "authorization" || name == "cookie" {
			return errForbiddenHeader
		}
		if name == "host" && !HostHeader(v) {
			return errors.New("Host header must be a host or host:port")
		}
	}
	return nil
}

// HTTPHeaderEnvs checks request headers whose values the executor reads from
// the environment. Names follow HTTPHeaders; Authorization and Cookie are
// refused for the same reason, and a name may not also carry a literal value
// in requestHeaders, whatever its case. A Host entry is allowed: its resolved
// value is checked as a host by the probe. Errors contain no header or
// variable name.
func HTTPHeaderEnvs(requestHeaders, envs map[string]string) error {
	named := make(map[string]bool, len(requestHeaders)+len(envs))
	for k := range requestHeaders {
		named[strings.ToLower(k)] = true
	}
	for name, env := range envs {
		if !httpToken(name) {
			return errors.New("invalid HTTP header name")
		}
		lower := strings.ToLower(name)
		if named[lower] {
			return errors.New("duplicate HTTP header name")
		}
		named[lower] = true
		if lower == "authorization" || lower == "cookie" {
			return errForbiddenHeader
		}
		if !SecretEnvName(env) {
			return errors.New("invalid secret environment variable name")
		}
	}
	return nil
}

// SecretEnvName reports whether name can name an environment variable holding
// a secret. It is deliberately broader than POSIX shell identifiers so that
// existing bearer_env spellings keep working: 1..128 bytes, no whitespace,
// no '=' (which a lookup would never match), no comma (the editor's list
// separator) and no control or bidi rune.
func SecretEnvName(name string) bool {
	return name != "" && len(name) <= 128 && secure.Text(name) == name &&
		!strings.ContainsAny(name, "=,") &&
		strings.IndexFunc(name, unicode.IsSpace) < 0
}

func httpToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

// HTTPHeaderValue reports whether v can be sent as an HTTP field value: bytes
// below 32 except HTAB, and no DEL. HTTP allows non-ASCII bytes here; Host is
// narrower.
func HTTPHeaderValue(v string) bool {
	for _, c := range []byte(v) {
		if c < 32 && c != '\t' || c == 127 {
			return false
		}
	}
	return true
}

// HostHeader reports whether v is an outbound HTTP Host value: a reg-name, an
// IPv4 address or a bracketed IPv6 literal, with an optional port. It rejects
// what net/http would drop or send in an unintended form: control or space
// bytes, raw non-ASCII, quotes and angle brackets, unbalanced brackets,
// unbracketed IPv6, and empty or out-of-range ports.
func HostHeader(v string) bool {
	if v == "" || strings.HasPrefix(v, "-") {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= ' ' || c == 127 || c >= 0x80 || c == '<' || c == '>' || c == '"' {
			return false
		}
	}
	var host, port string
	if strings.HasPrefix(v, "[") {
		if strings.Count(v, "[") != 1 || strings.Count(v, "]") != 1 {
			return false
		}
		end := strings.IndexByte(v, ']')
		if end < 2 {
			return false
		}
		host, port = v[1:end], ""
		if rest := v[end+1:]; rest != "" {
			if len(rest) < 2 || rest[0] != ':' {
				return false
			}
			port = rest[1:]
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() {
			return false
		}
	} else {
		if strings.ContainsAny(v, "[]") {
			return false
		}
		if strings.Count(v, ":") > 1 {
			return false
		}
		host = v
		if i := strings.IndexByte(v, ':'); i >= 0 {
			if i == 0 || i == len(v)-1 {
				return false
			}
			host, port = v[:i], v[i+1:]
		}
		if !hostRegName(host) {
			return false
		}
	}
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return false
		}
	}
	u, e := url.Parse("http://" + v)
	return e == nil && u.Host == v && u.Hostname() != "" && host != ""
}

// hostRegName is the RFC 7230 reg-name set net/http will send: unreserved
// characters, sub-delimiters and percent escapes. Host and port are split
// first, so ':' is not part of it here.
func hostRegName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '.' || c == '-' || c == '_' || c == '~' || c == '%':
		case strings.ContainsRune("!$&'()*+,;=", rune(c)):
		default:
			return false
		}
	}
	return true
}
