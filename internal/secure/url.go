package secure

import (
	"net/url"
	"strings"
)

func PublicURL(s string) string {
	s, _, _ = strings.Cut(s, "#")
	if u, e := url.Parse(s); e == nil && u.Scheme != "" && u.Opaque == "" {
		u.User = nil
		if u.RawQuery != "" {
			u.RawQuery = "redacted"
		}
		return u.String()
	}
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if before, _, ok := strings.Cut(s, "?"); ok {
		s = before + "?redacted"
	}
	return s
}
