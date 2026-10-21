package config

import (
	"net/url"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// HasLiteralSecret reports whether c stores a secret value, not merely the
// name of an environment variable that holds one. A topology without one may
// stay group-readable; a topology with one may not.
func HasLiteralSecret(c model.Config) bool {
	for _, e := range c.Endpoints {
		if urlCarriesSecret(e.Address) {
			return true
		}
	}
	for _, p := range c.Paths {
		for _, proxy := range p.Proxies {
			if urlCarriesSecret(proxy.Address) {
				return true
			}
		}
	}
	for _, x := range c.Checks {
		if len(x.HTTP.RequestHeaders) > 0 || urlCarriesSecret(x.Target) {
			return true
		}
		if ref := x.Adapter.ProviderReference; ref != nil && urlCarriesSecret(ref.URL) {
			return true
		}
	}
	return false
}

func urlCarriesSecret(s string) bool {
	if s == "" {
		return false
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Opaque == "" {
		return u.User != nil || u.RawQuery != ""
	}
	return strings.Contains(s, "@") || strings.Contains(s, "?")
}
