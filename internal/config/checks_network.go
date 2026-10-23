package config

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflared"
	"github.com/WhitenWhiten/topophany/internal/adapters/tailscale"
	"github.com/WhitenWhiten/topophany/internal/adapters/wireguard"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func validateStreamCheck(c model.Config, x model.Check) error {
	t := Target(c, x)
	if e := HostPort(t); e != nil {
		return fmt.Errorf("check %s: %w", x.ID, e)
	}

	return nil
}

func validateICMPCheck(c model.Config, x model.Check) error {
	t := Target(c, x)
	if x.ResolveIP != "" {
		return fmt.Errorf("check %s: ICMP already requires a literal target; resolve_ip is unsupported", x.ID)
	}
	if ip, e := netip.ParseAddr(t); e != nil || !ip.Is4() {
		return errors.New("ICMP currently requires an explicit IPv4 target")
	}
	if ip, _ := netip.ParseAddr(x.BindIP); x.BindIP != "" && !ip.Is4() {
		return fmt.Errorf("check %s: ICMP binds only an IPv4 address", x.ID)
	}

	return nil
}

func validateTailscaleCheck(c model.Config, x model.Check) error {
	if x.Adapter.TailscaleDiagnostic != "" && x.TimeoutSeconds < 4 {
		return errors.New("tailscale_diagnostic needs timeout_seconds of at least 4")
	}
	if err := tailscale.Validate(x.Adapter.Tailscale()); err != nil {
		return err
	}

	return nil
}

func validateWireGuardCheck(c model.Config, x model.Check) error {
	if err := wireguard.Validate(x.Adapter.WireGuard()); err != nil {
		return err
	}

	return nil
}

func validateCloudflaredCheck(c model.Config, x model.Check) error {
	t := Target(c, x)
	// The connector's metrics listener is a local process: no host
	// override, no source binding, no proxy chain, no path.
	if x.Path != "" {
		return fmt.Errorf("check %s: cloudflared reads a literal loopback listener; path is unsupported", x.ID)
	}
	if x.BindIP != "" || x.ResolveIP != "" {
		return fmt.Errorf("check %s: cloudflared does not accept bind_ip or resolve_ip", x.ID)
	}
	if err := cloudflared.ValidateTarget(t, x.CAFile); err != nil {
		return fmt.Errorf("check %s: %w", x.ID, err)
	}
	if err := cloudflared.Validate(x.Adapter.CloudflaredOptions()); err != nil {
		return fmt.Errorf("check %s: %w", x.ID, err)
	}

	return nil
}

func validateCloudflareCheck(c model.Config, x model.Check) error {
	t := Target(c, x)
	// The API read sends a token and is at most one GET per check.
	// The default target is the documented v4 base; a test may point
	// it at a literal loopback server over http.
	if x.IntervalSeconds < 60 {
		return fmt.Errorf("check %s: cloudflare API reads require interval_seconds of at least 60", x.ID)
	}
	if x.Endpoint != "" {
		return fmt.Errorf("check %s: cloudflare reads the API at target (default https://api.cloudflare.com/client/v4), not an endpoint", x.ID)
	}
	if x.Adapter.TokenEnv == "" || !SecretEnvName(x.Adapter.TokenEnv) {
		return fmt.Errorf("check %s: cloudflare API reads require a valid adapter.token_env", x.ID)
	}
	if err := cloudflare.ValidateTarget(t); err != nil {
		return fmt.Errorf("check %s: %w", x.ID, err)
	}
	if err := cloudflare.Validate(x.Adapter.CloudflareOptions()); err != nil {
		return fmt.Errorf("check %s: %w", x.ID, err)
	}
	// token_env is a credential: it needs HTTPS or direct literal
	// loopback without a proxy or remote resolve override.
	if err := HTTPCredentials(c, x); err != nil {
		return err
	}

	return nil
}
