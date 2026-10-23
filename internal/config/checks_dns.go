package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func validateDNSCheck(c model.Config, x model.Check) error {
	t := Target(c, x)
	// The transport decides the target rule: the three stream/one
	// datagram transports take a resolver host:port (Dot is usually
	// 853, but any port is the operator's), while DoH takes an https
	// URL whose path may select the endpoint. An unknown transport is
	// refused rather than silently executed as UDP.
	switch x.DNS.Transport {
	case "", "udp", "tcp", "dot":
		if e := HostPort(t); e != nil {
			return fmt.Errorf("check %s: %w", x.ID, e)
		}
	case "doh":
		u, e := url.Parse(t)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return fmt.Errorf("check %s: a doh target must be an https URL with a host and optional path, without userinfo, query or fragment", x.ID)
		}
	default:
		return fmt.Errorf("check %s: dns transport must be empty, udp, tcp, dot or doh", x.ID)
	}

	if len(x.DNS.Name) < 1 || len(x.DNS.Name) > 253 {
		return errors.New("DNS name required")
	}
	for _, label := range strings.Split(strings.TrimSuffix(x.DNS.Name, "."), ".") {
		if len(label) == 0 || len(label) > 63 || strings.IndexFunc(label, func(r rune) bool { return r <= 32 || r >= 127 || r == '\\' }) >= 0 {
			return errors.New("invalid DNS label")
		}
	}
	if x.DNS.Type != "A" && x.DNS.Type != "AAAA" {
		return errors.New("DNS type must be A or AAAA")
	}
	for _, ip := range x.DNS.ExpectedIPs {
		if _, e := netip.ParseAddr(ip); e != nil {
			return errors.New("invalid expected DNS IP")
		}
	}
	return nil
}
