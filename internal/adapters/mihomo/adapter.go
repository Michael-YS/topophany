// Package mihomo collects read-only Mihomo runtime telemetry.
package mihomo

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Dependencies are supplied by the probe, preserving its explicit route and TLS policy.
type Dependencies struct {
	Client           func(model.Check, string) (*http.Client, func(), error)
	Proxied          bool
	ProxyCredentials bool
}

func Collect(ctx context.Context, deps Dependencies, x model.Check, target string, o *model.Observation) error {
	options := x.Adapter.Mihomo()
	token, e := secure.SecretEnv(options.TokenEnv)
	if e != nil {
		return adapters.Local("setup", "adapter_secret_missing")
	}
	u, e := url.Parse(target)
	if e != nil {
		return adapters.Local("setup", "invalid_adapter_url")
	}
	// A controller sent a secret says its mode in a closed word and names
	// only what the topology declares; see withheld.
	secret := token != "" || u.Scheme != "https" && deps.ProxyCredentials
	mode, name := adapters.SafeFact, func(s string) string { return s }
	if secret {
		known := declared(options)
		mode = closedMode
		name = func(s string) string {
			if known[s] {
				return s
			}
			return "undeclared"
		}
	}
	client, closeClient, e := deps.Client(x, target)
	if e != nil {
		return e
	}
	defer closeClient()
	var cfg struct {
		Mode string `json:"mode"`
		TUN  struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if e = readCore(ctx, client, target, "/configs", token, &cfg); e != nil {
		return e
	}
	if cfg.Mode == "" {
		return adapters.Local("adapter_api", "missing_mode")
	}
	var proxies struct {
		Proxies map[string]coreProxy `json:"proxies"`
	}
	if e = readCore(ctx, client, target, "/proxies", token, &proxies); e != nil {
		return e
	}
	if proxies.Proxies == nil {
		return adapters.Local("adapter_api", "missing_proxies")
	}
	o.PathEvidence = "runtime_reported_not_request_verified"
	o.Facts["sampling_atomicity"] = "separate_API_reads_not_atomic"
	o.Facts["mode"] = mode(cfg.Mode)
	if secret {
		o.Facts["peer_text"] = adapters.Withheld
	}
	o.Facts["tun_enabled"] = strconv.FormatBool(cfg.TUN.Enable)
	o.Facts["provider_parameters"] = "not_verifiable_by_names_alone"
	o.Outcome = "ok"
	o.Stage = "adapter"
	o.Detail = "read-only runtime telemetry collected; not an end-to-end service check"
	if options.ExpectedMode != "" && cfg.Mode != options.ExpectedMode {
		o.Outcome = "policy"
		o.Code = "mode_mismatch"
		o.Detail = "runtime mode differs from declared policy"
	}
	if options.RootProxy != "" {
		chain, e := expandProxy(proxies.Proxies, options.RootProxy)
		if e != nil {
			o.Facts["chain_resolution"] = "unresolved"
			if o.Outcome != "policy" {
				o.Outcome = "unknown"
				o.Code = "runtime_chain_unresolved"
				o.Detail = "runtime chain unresolved (missing selection, unsupported shape or cycle)"
			}
		} else {
			for i := range chain {
				chain[i] = name(chain[i])
			}
			o.Facts["reported_chain"] = adapters.SafeFact(strings.Join(chain, " -> "))
		}
		if p, ok := proxies.Proxies[options.RootProxy]; ok {
			o.Facts["reported_selection"] = adapters.SafeFact(name(p.Now))
			if !secret {
				o.Facts["test_url"] = adapters.PublicURL(p.TestURL)
			}
			if len(p.History) > 0 {
				last := p.History[len(p.History)-1]
				o.Facts["core_last_checked"] = last.Time.UTC().Format(time.RFC3339)
				o.Facts["core_delay_ms"] = strconv.Itoa(last.Delay)
			}
		}
	}
	return collectProviders(ctx, deps, client, x, target, token, name, o)
}
