package config

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"time"

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

// ObservationBounds says what is wrong with an observation's values, as
// the code a collector refuses it with, or "" if nothing: what a worker
// may send a collector and a collector a viewer alike. It took no time
// below zero or past took, its check's timeout and five seconds; its
// outcome is one of the closed list; and its diagnostics are short, plain
// text. Its identity and its place are checked by whoever reads it.
func ObservationBounds(v model.Observation, took time.Duration) string {
	limit := took.Milliseconds()
	switch v.Outcome {
	case "ok", "fail", "unknown", "error", "policy":
	default:
		return "invalid_outcome"
	}
	if v.DurationMS < 0 || v.DurationMS > limit || len(v.Detail) > 1024 || len(v.Code) > 96 || len(v.Stage) > 96 || len(v.PathEvidence) > 128 || len(v.Facts) > 32 || len(v.Timings) > 16 {
		return "observation_budget_exceeded"
	}
	for k, val := range v.Facts {
		if len(k) > 128 || len(val) > 2048 || secure.Text(k) != k || secure.Text(val) != val {
			return "invalid_fact"
		}
	}
	for k, n := range v.Timings {
		if len(k) > 64 || secure.Text(k) != k || n < 0 || n > limit {
			return "invalid_timing"
		}
	}
	if secure.Text(v.Detail) != v.Detail || secure.Text(v.Code) != v.Code || secure.Text(v.Stage) != v.Stage || secure.Text(v.PathEvidence) != v.PathEvidence {
		return "unsafe_diagnostic_text"
	}
	return ""
}

// MaxTook is the longest any sample may take: the longest timeout a check
// may have, and five seconds.
const MaxTook = (MaxTimeoutSeconds + 5) * time.Second

// MaxTimeoutSeconds is the longest timeout a check may have.
const MaxTimeoutSeconds = 300

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
