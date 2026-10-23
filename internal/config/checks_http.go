package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"

	"github.com/WhitenWhiten/topophany/internal/adapters/mihomo"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func validateHTTPCheck(c model.Config, x model.Check) error {
	t := Target(c, x)
	u, e := url.Parse(t)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("check %s: requires HTTP(S) URL without userinfo/fragment", x.ID)
	}
	if e := HTTPCredentials(c, x); e != nil {
		return e
	}
	if x.HTTP.Method != "GET" && x.HTTP.Method != "HEAD" {
		return fmt.Errorf("check %s: only read-only HTTP methods allowed", x.ID)
	}
	for _, code := range x.HTTP.Status {
		if code < 100 || code > 599 {
			return errors.New("invalid HTTP status assertion")
		}
	}
	if x.HTTP.MaxBodyBytes < 1 || x.HTTP.MaxBodyBytes > 1<<20 {
		return errors.New("HTTP body budget must be 1..1048576")
	}
	if _, e := CompileBodyRegex(x.HTTP.BodyRegex); e != nil {
		return fmt.Errorf("check %s: invalid response regex", x.ID)
	}
	if e := HTTPHeaders(x.HTTP.RequestHeaders); e != nil {
		return fmt.Errorf("check %s: %w", x.ID, e)
	}
	if e := HTTPHeaderEnvs(x.HTTP.RequestHeaders, x.HTTP.RequestHeaderEnvs); e != nil {
		return fmt.Errorf("check %s: %w", x.ID, e)
	}
	if x.HTTP.BearerEnv != "" && !SecretEnvName(x.HTTP.BearerEnv) {
		return fmt.Errorf("check %s: invalid bearer environment variable name", x.ID)
	}
	for _, v := range x.HTTP.ExpectedExit {
		if _, e := netip.ParseAddr(v); e != nil {
			if _, e = netip.ParsePrefix(v); e != nil {
				return errors.New("expected_exit must contain IPs/CIDRs")
			}
		}
	}
	if len(x.HTTP.ExpectedPops) > 16 {
		return errors.New("at most 16 expected pops")
	}
	seenPops := make(map[string]bool, len(x.HTTP.ExpectedPops))
	for _, pop := range x.HTTP.ExpectedPops {
		if !model.IsColo(pop) || seenPops[pop] {
			return errors.New("expected_pops must be three uppercase letters, without duplicates")
		}
		seenPops[pop] = true
	}
	if x.HTTP.ExitFormat != "" && x.HTTP.ExitFormat != "plain" && x.HTTP.ExitFormat != "trace" && x.HTTP.ExitFormat != "json" {
		return errors.New("invalid exit_format")
	}
	if trace := x.HTTP.ExpectedTrace; trace != nil {
		// The probe parses a trace body only for exit_format trace, so
		// an expectation under any other format could never be
		// evaluated.
		if x.HTTP.ExitFormat != "trace" {
			return errors.New("expected_trace requires exit_format trace")
		}
		// An empty expectation would assert nothing while still being a
		// distinct topology value, so the editor could silently change
		// a contract the user did not edit by dropping it.
		if trace.Warp == "" && trace.Gateway == "" && len(trace.Colos) == 0 {
			return errors.New("expected_trace must declare warp, gateway or colos")
		}
		switch trace.Warp {
		case "", "off", "on", "plus":
		default:
			return errors.New("invalid trace warp")
		}
		switch trace.Gateway {
		case "", "on", "off":
		default:
			return errors.New("invalid trace gateway")
		}
		if len(trace.Colos) > 16 {
			return errors.New("at most 16 expected trace colos")
		}
		seen := make(map[string]bool, len(trace.Colos))
		for _, colo := range trace.Colos {
			if !model.IsColo(colo) || seen[colo] {
				return errors.New("trace colos must be three uppercase letters, without duplicates")
			}
			seen[colo] = true
		}
	}

	return nil
}

func validateMihomoCheck(c model.Config, x model.Check) error {
	if err := validateHTTPCheck(c, x); err != nil {
		return err
	}
	if err := mihomo.Validate(x.Adapter.Mihomo()); err != nil {
		return err
	}
	if ref := x.Adapter.ProviderReference; ref != nil {
		if err := mihomo.ValidateReferenceTransport(ref, len(Path(c, x.Path).Proxies) > 0); err != nil {
			return err
		}
	}

	return nil
}
