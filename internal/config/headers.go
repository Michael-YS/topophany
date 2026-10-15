package config

import (
	"errors"
	"strings"
)

// HTTPHeaders checks the wire syntax before a check can run. Values may
// contain HTAB and non-ASCII bytes, but no other controls. Names are HTTP
// tokens. Errors deliberately contain neither header names nor values.
func HTTPHeaders(headers map[string]string) error {
	named := make(map[string]bool, len(headers))
	for k, v := range headers {
		if k == "" {
			return errors.New("invalid HTTP header name")
		}
		for _, c := range []byte(k) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
				return errors.New("invalid HTTP header name")
			}
		}
		for _, c := range []byte(v) {
			if c < 32 && c != '\t' || c == 127 {
				return errors.New("invalid HTTP header value")
			}
		}
		name := strings.ToLower(k)
		if named[name] {
			return errors.New("duplicate HTTP header name")
		}
		named[name] = true
		if name == "authorization" || name == "cookie" {
			return errors.New("use bearer_env; literal Authorization/Cookie forbidden")
		}
		if name == "host" && !hostHeader(v) {
			return errors.New("Host header must be a host or host:port")
		}
	}
	return nil
}
