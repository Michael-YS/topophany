package config

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// PresentationBounds applies equally to local topology and remote public views.
// Total cardinality bounds the renderer's pairwise layout work as well as wire size.
func PresentationBounds(c model.Config) error {
	if len(c.Nodes)+len(c.Endpoints)+len(c.Services)+len(c.Observers)+len(c.Links)+len(c.Paths)+len(c.Checks) > 4096 {
		return errors.New("topology exceeds 4096 total entities")
	}
	references := len(c.Links) * 2
	for _, n := range c.Nodes {
		if n.Parent != "" {
			references++
		}
	}
	for _, p := range c.Paths {
		references += len(p.Nodes) + len(p.Links) + len(p.Candidates) + len(p.DependsOn) + len(p.Proxies)
	}
	for _, s := range c.Services {
		references += len(s.Endpoints) + len(s.DependsOn)
	}
	for _, e := range c.Endpoints {
		references += len(e.Via)
	}
	if references > 8192 {
		return errors.New("topology exceeds 8192 graph references")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("topology exceeds 1 MiB presentation budget")
	}
	return secure.JSONBudget(raw)
}

func literalLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// A later loopback hop belongs to the previous proxy's namespace; it is not
// necessarily local and its forwarding leg may be plaintext. Only direct
// literal loopback is exempt. TLS to a previous hop does not protect later legs.
func ProxyCredentials(p model.Proxy, index int) error {
	if (p.Kind == "http-connect" || p.Kind == "socks5") && (p.Username != "" || p.PasswordEnv != "") {
		host, _, err := net.SplitHostPort(p.Address)
		if err != nil || index != 0 || !literalLoopback(host) {
			return errors.New("proxy credentials require HTTPS CONNECT or direct literal loopback")
		}
	}
	return nil
}

// HTTPCredentials refuses a check that sends a credential, a bearer or
// adapter token or a target's query, where a hop it was not meant for can
// read it.
func HTTPCredentials(c model.Config, x model.Check) error {
	if x.HTTP.BearerEnv == "" && x.Adapter.TokenEnv == "" && !TargetQuery(c, x) {
		return nil
	}
	u, err := url.Parse(Target(c, x))
	if err != nil {
		return errors.New("invalid secret-bearing URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if x.ResolveIP != "" {
		host = x.ResolveIP
	}
	if u.Scheme == "http" && literalLoopback(u.Hostname()) && literalLoopback(host) && len(Path(c, x.Path).Proxies) == 0 {
		return nil
	}
	return errors.New("HTTP credentials and target queries require HTTPS or direct literal loopback without a remote resolve override")
}

// TargetQuery reports whether an http check sends a query with its target.
// Viewers never see a query (PublicURL), as it may hold a credential, so it
// is sent as one, never in the clear beyond loopback, and what the peer
// answers is kept as for one. A Mihomo check sends none.
func TargetQuery(c model.Config, x model.Check) bool {
	u, err := url.Parse(Target(c, x))
	return x.Kind == "http" && err == nil && u.RawQuery != ""
}

// ValidatePublic restores only redacted SSH login and file placeholders on an
// owned copy; the public topology is never executable and must still pass all
// graph checks.
func ValidatePublic(c model.Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	var copy model.Config
	if err := json.Unmarshal(raw, &copy); err != nil {
		return err
	}
	for i := range copy.Paths {
		for j := range copy.Paths[i].Proxies {
			p := &copy.Paths[i].Proxies[j]
			if p.Kind == "ssh" {
				p.SSHUser = "redacted"
				p.SSHIdentityFile = "redacted"
				p.SSHKnownHosts = "redacted"
			}
		}
	}
	return Validate(copy)
}
