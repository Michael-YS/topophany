package config

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/model"
	"net/netip"
	"slices"
	"strings"
)

// Addresses are literal IPs or DNS hostnames, not environment variables,
// URLs, credentials, shell expressions or recursive templates.
func validateAddresses(values map[string]string) error {
	if len(values) > 256 {
		return errors.New("at most 256 address definitions")
	}
	for id, value := range values {
		if !ID(id) || len(value) > 253 {
			return errors.New("invalid address definition")
		}
		if ip, e := netip.ParseAddr(value); e == nil && ip.Zone() == "" {
			continue
		}
		for _, label := range strings.Split(value, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return errors.New("address must be a literal IP or DNS hostname")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return errors.New("address must be a literal IP or DNS hostname")
				}
			}
		}
	}
	return nil
}
func expandAddress(values map[string]string, s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var out strings.Builder
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			out.WriteString(s)
			break
		}
		out.WriteString(s[:start])
		s = s[start+2:]
		end := strings.IndexByte(s, '}')
		if end < 0 {
			return "", errors.New("unterminated address reference")
		}
		value, ok := values[s[:end]]
		if !ok {
			return "", errors.New("unknown address reference")
		}
		out.WriteString(value)
		s = s[end+1:]
		if out.Len() > 8192 {
			return "", errors.New("expanded network field exceeds 8192 bytes")
		}
	}
	if out.Len() > 8192 {
		return "", errors.New("expanded network field exceeds 8192 bytes")
	}
	return out.String(), nil
}
func ResolveCheck(c model.Config, x model.Check) (model.Check, error) {
	if err := validateAddresses(c.Addresses); err != nil {
		return x, err
	}
	return resolveCheck(c, x)
}
func resolveCheck(c model.Config, x model.Check) (model.Check, error) {
	if c.Addresses == nil && !checkHasReferences(x) {
		return x, nil
	}
	x.HTTP.ExpectedExit = slices.Clone(x.HTTP.ExpectedExit)
	x.DNS.ExpectedIPs = slices.Clone(x.DNS.ExpectedIPs)
	fields := []*string{&x.Target, &x.BindIP, &x.ResolveIP, &x.ServerName, &x.DNS.Name, &x.Adapter.Peer}
	for i := range x.HTTP.ExpectedExit {
		fields = append(fields, &x.HTTP.ExpectedExit[i])
	}
	for i := range x.DNS.ExpectedIPs {
		fields = append(fields, &x.DNS.ExpectedIPs[i])
	}
	for _, field := range fields {
		value, e := expandAddress(c.Addresses, *field)
		if e != nil {
			return x, e
		}
		*field = value
	}
	return x, nil
}
func resolvePath(c model.Config, p model.Path) (model.Path, error) {
	if c.Addresses == nil {
		found := false
		for _, proxy := range p.Proxies {
			found = found || strings.Contains(proxy.Address, "${") || strings.Contains(proxy.ServerName, "${")
		}
		if !found {
			return p, nil
		}
	}
	p.Proxies = slices.Clone(p.Proxies)
	for i := range p.Proxies {
		for _, field := range []*string{&p.Proxies[i].Address, &p.Proxies[i].ServerName} {
			value, e := expandAddress(c.Addresses, *field)
			if e != nil {
				return p, e
			}
			*field = value
		}
	}
	return p, nil
}

// ResolveAddresses returns a detached executable/presentation configuration.
// The declared configuration, editor saves and bundles retain their references.
func ResolveAddresses(c model.Config) (model.Config, error) {

	if e := validateAddresses(c.Addresses); e != nil {
		return c, e
	}
	out := Clone(c)
	for i := range out.Endpoints {
		value, e := expandAddress(c.Addresses, out.Endpoints[i].Address)
		if e != nil {
			return c, e
		}
		out.Endpoints[i].Address = value
	}
	for i := range out.Checks {
		x, e := resolveCheck(c, out.Checks[i])
		if e != nil {
			return c, e
		}
		out.Checks[i] = x
	}
	for i := range out.Paths {
		p, e := resolvePath(c, out.Paths[i])
		if e != nil {
			return c, e
		}
		out.Paths[i] = p
	}
	// Resolved copies carry no aliases, avoiding any second interpretation.
	out.Addresses = nil
	return out, nil
}

func checkHasReferences(x model.Check) bool {
	for _, s := range []string{x.Target, x.BindIP, x.ResolveIP, x.ServerName, x.DNS.Name, x.Adapter.Peer} {
		if strings.Contains(s, "${") {
			return true
		}
	}
	for _, s := range x.HTTP.ExpectedExit {
		if strings.Contains(s, "${") {
			return true
		}
	}
	for _, s := range x.DNS.ExpectedIPs {
		if strings.Contains(s, "${") {
			return true
		}
	}
	return false
}
