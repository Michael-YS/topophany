package probe

import (
	"context"
	"net"
	"net/http"
	"net/url"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/adapters/mihomo"
	"github.com/WhitenWhiten/topophany/internal/adapters/tailscale"
	"github.com/WhitenWhiten/topophany/internal/adapters/wireguard"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// The supplied transport preserves the check's explicit route and TLS policy.
func (r Runner) mihomo(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	return mihomo.Collect(ctx, mihomo.Dependencies{Client: func(x model.Check, target string) (*http.Client, func(), error) {
		u, err := url.Parse(target)
		if err != nil {
			return nil, nil, local("setup", "invalid_adapter_url")
		}
		name := u.Hostname()
		if x.ServerName != "" {
			name = x.ServerName
		}
		tc, err := TLSConfig(name, x.CAFile)
		if err != nil {
			return nil, nil, err
		}
		tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, TLSClientConfig: tc, MaxResponseHeaderBytes: 32768, DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
			return d.DialContext(ctx, n, pinned(a, x.ResolveIP))
		}}
		return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr.CloseIdleConnections, nil
	}, Proxied: len(d.Proxies) > 0, ProxyCredentials: proxyCredentials(d.Proxies)}, x, target, o)
}
func (r Runner) tailscale(ctx context.Context, x model.Check, o *model.Observation) error {
	return tailscale.Collect(ctx, r.command, x.Adapter.Tailscale(), o)
}
func (r Runner) wireguard(ctx context.Context, x model.Check, o *model.Observation) error {
	return wireguard.Collect(ctx, r.command, x.Adapter.WireGuard(), o)
}
func safeFact(s string) string  { return adapters.SafeFact(s) }
func publicURL(s string) string { return adapters.PublicURL(s) }
