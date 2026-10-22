// Package sourceutil provides the shared safety boundaries for external-source
// adapters. It never opens a transport, resolves credentials or interprets a
// vendor's endpoints, object IDs, report types or metric semantics.
package sourceutil

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// SourcePolicy is supplied by adapter code, never by upstream data. Adapter
// and BaseName preserve each adapter's fixed validation diagnostics.
type SourcePolicy struct {
	Adapter            string
	BaseName           string
	CredentialOptional bool
}

// ValidateSource accepts a simple server base URL and an explicit credential
// reference. Plain HTTP is permitted only on a direct literal loopback route.
func ValidateSource(s model.Source, policy SourcePolicy) error {
	u, err := url.Parse(s.Target)
	if err != nil || len(s.Target) > model.MaxSourceTarget || secure.Text(s.Target) != s.Target ||
		u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(s.Target, "#") || u.RawPath != "" ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%s target must be an HTTP(S) %s base URL without credentials, query or fragment", policy.Adapter, policy.BaseName)
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return fmt.Errorf("%s target port must be 1..65535", policy.Adapter)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%s target port must be 1..65535", policy.Adapter)
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%s target path must not contain dot segments", policy.Adapter)
		}
		for _, c := range segment {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
				return fmt.Errorf("%s target path must contain only unescaped URL path segments", policy.Adapter)
			}
		}
	}
	if strings.Contains(u.Path, "//") || strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api") {
		return fmt.Errorf("%s target must be the %s base URL before /api", policy.Adapter, policy.BaseName)
	}
	if (!policy.CredentialOptional || s.CredentialEnv != "") && !environmentName(s.CredentialEnv) {
		return fmt.Errorf("%s credential_env must name an environment variable", policy.Adapter)
	}
	if u.Scheme == "http" && (!loopback(u.Hostname()) || s.Path != "" || s.ResolveIP != "" && !loopback(s.ResolveIP)) {
		return fmt.Errorf("%s requires HTTPS except for direct literal loopback HTTP", policy.Adapter)
	}
	return nil
}

func environmentName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func loopback(host string) bool {
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && ip.IsLoopback()
}

// ValidCredential checks a raw header credential within vendor-owned length
// limits. Prefixes such as Bearer remain the adapter's responsibility.
func ValidCredential(token string, minimum, maximum int) bool {
	if minimum < 1 || maximum < minimum || len(token) < minimum || len(token) > maximum {
		return false
	}
	for _, c := range token {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}
