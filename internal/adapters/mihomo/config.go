package mihomo

import (
	"errors"
	"net"
	"net/url"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func Validate(options model.MihomoOptions) error {
	if len(options.ExpectedProviders) > 16 {
		return errors.New("maximum 16 expected providers per check")
	}
	for k := range options.ExpectedProviders {
		if len(k) > 119 || secure.Text(k) != k {
			return errors.New("provider name exceeds evidence budget or contains control characters")
		}
	}
	if ref := options.ProviderReference; ref != nil {
		if err := ValidateReferenceTransport(ref, false); err != nil {
			return err
		}
		if ref.MaxAgeSeconds < 1 || ref.MaxAgeSeconds > 604800 || len(ref.Providers) == 0 || len(options.ExpectedProviders)+len(ref.Providers) > 16 {
			return errors.New("provider reference requires 1..604800 max_age_seconds and 1..16 total expected providers")
		}
		seen := map[string]bool{}
		for _, name := range ref.Providers {
			_, fixed := options.ExpectedProviders[name]
			if name == "" || len(name) > 119 || secure.Text(name) != name || seen[name] || fixed {
				return errors.New("provider reference names must be valid, unique and separate from static expectations")
			}
			seen[name] = true
		}
	}
	for name, mode := range options.ProviderMatchModes {
		_, known := options.ExpectedProviders[name]
		if ref := options.ProviderReference; ref != nil {
			for _, provider := range ref.Providers {
				if provider == name {
					known = true
				}
			}
		}
		if !known || (mode != "ordered" && mode != "set") {
			return errors.New("provider_match_modes must name an expected provider with ordered or set comparison")
		}
		if mode == "set" {
			seen := map[string]bool{}
			for _, member := range options.ExpectedProviders[name] {
				if seen[member] {
					return errors.New("set provider expectations must not contain duplicate members")
				}
				seen[member] = true
			}
		}
	}

	return nil
}

// ValidateReferenceTransport excludes plaintext remote publishers, including
// loopback URLs reached through a remote proxy's network namespace.
func ValidateReferenceTransport(ref *model.MihomoProviderReference, proxied bool) error {
	u, err := url.Parse(ref.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || secure.Text(ref.URL) != ref.URL {
		return errors.New("provider reference requires an HTTP(S) URL without credentials, query or fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && !proxied && net.ParseIP(u.Hostname()).IsLoopback() {
		return nil
	}
	return errors.New("provider reference requires HTTPS or direct literal loopback")
}
