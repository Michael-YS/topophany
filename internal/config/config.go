// Package config loads a strict, versioned JSON topology. No remote configuration
// is executed: each executor uses its own reviewed local copy.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)

func ID(s string) bool { return identifier.MatchString(s) }
func Decode(r io.Reader) (model.Config, error) {
	var c model.Config
	b, err := io.ReadAll(io.LimitReader(r, 4<<20+1))
	if err != nil {
		return c, err
	}
	if len(b) > 4<<20 {
		return c, errors.New("topology exceeds 4 MiB")
	}
	if err := secure.JSONBudget(b); err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid topology JSON: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return c, errors.New("topology must contain one JSON object")
	}
	Defaults(&c)
	return c, Validate(c)
}
func Load(path string) (model.Config, error) {
	f, e := os.Open(path)
	if e != nil {
		return model.Config{}, e
	}
	defer f.Close()
	return Decode(f)
}
func Defaults(c *model.Config) {
	for i := range c.Observers {
		if c.Observers[i].StaleSeconds == 0 {
			c.Observers[i].StaleSeconds = 90
		}
	}
	for i := range c.Checks {
		x := &c.Checks[i]
		if x.IntervalSeconds == 0 {
			x.IntervalSeconds = 60
		}
		if x.TimeoutSeconds == 0 {
			x.TimeoutSeconds = 8
		}
		if x.StaleSeconds == 0 {
			x.StaleSeconds = x.IntervalSeconds * 3
		}
		if x.FailureThreshold == 0 {
			x.FailureThreshold = 3
		}
		if x.RecoveryThreshold == 0 {
			x.RecoveryThreshold = 2
		}
		if x.HTTP.Method == "" {
			x.HTTP.Method = "GET"
		}
		if x.HTTP.MaxBodyBytes == 0 {
			x.HTTP.MaxBodyBytes = 65536
		}
		if x.DNS.Type == "" {
			x.DNS.Type = "A"
		}
	}
}
func Validate(c model.Config) error {
	if err := PresentationBounds(c); err != nil {
		return err
	}
	resolved, err := ResolveAddresses(c)
	if err != nil {
		return err
	}
	return validateResolved(resolved)
}
func validateResolved(c model.Config) error {
	if c.Schema != model.ConfigSchema {
		return fmt.Errorf("unsupported schema: %d", c.Schema)
	}
	if !ID(c.Project) {
		return errors.New("invalid project ID")
	}
	if e := PresentationBounds(c); e != nil {
		return e
	}
	if err := validateApplications(c); err != nil {
		return err
	}
	nodes := map[string]bool{}
	ends := map[string]model.Endpoint{}
	services := map[string]bool{}
	obs := map[string]bool{}
	paths := map[string]model.Path{}
	links := map[string]model.Link{}
	checks := map[string]bool{}
	unique := func(m map[string]bool, id, kind string) error {
		if !ID(id) || m[id] {
			return fmt.Errorf("invalid or duplicate %s ID %q", kind, id)
		}
		m[id] = true
		return nil
	}
	for _, n := range c.Nodes {
		if e := unique(nodes, n.ID, "node"); e != nil {
			return e
		}
	}
	for _, s := range c.Services {
		if e := unique(services, s.ID, "service"); e != nil {
			return e
		}
		if !nodes[s.Node] {
			return fmt.Errorf("service %s: missing node", s.ID)
		}
		if len(s.Runtime) > 32 || secure.Text(s.Runtime) != s.Runtime {
			return fmt.Errorf("service %s: runtime must be a short label", s.ID)
		}
	}
	for _, o := range c.Observers {
		if o.StorageWeight < 0 || o.StorageWeight > 1000 {
			return fmt.Errorf("observer %s: storage_weight must be 0..1000", o.ID)
		}
		if e := unique(obs, o.ID, "observer"); e != nil {
			return e
		}
		if o.Node != "" && !nodes[o.Node] {
			return fmt.Errorf("observer %s: missing node", o.ID)
		}
		if o.Context == "" || o.StaleSeconds < 5 || o.StaleSeconds > 86400 {
			return fmt.Errorf("observer %s: context and valid freshness required", o.ID)
		}
	}
	for _, n := range c.Nodes {
		if n.Parent != "" && !nodes[n.Parent] {
			return fmt.Errorf("node %s: missing parent", n.ID)
		}
		if l := n.Location; l != nil {
			if !(l.Lat >= -90 && l.Lat <= 90 && l.Lon >= -180 && l.Lon <= 180) || len(l.Label) > 64 || secure.Text(l.Label) != l.Label {
				return fmt.Errorf("node %s: location needs lat -90..90, lon -180..180 and a short label", n.ID)
			}
		}
		if len(n.Provider) > 64 || secure.Text(n.Provider) != n.Provider {
			return fmt.Errorf("node %s: provider must be a short label", n.ID)
		}
	}
	for _, ep := range c.Endpoints {
		if !ID(ep.ID) || ends[ep.ID].ID != "" || !nodes[ep.Node] || ep.Address == "" {
			return fmt.Errorf("invalid endpoint %q", ep.ID)
		}
		switch ep.Network {
		case "public", "lan", "tailnet", "external":
		case "loopback":
			if !obs[ep.Observer] {
				return fmt.Errorf("loopback endpoint %s needs an observer scope", ep.ID)
			}
		default:
			return fmt.Errorf("endpoint %s: invalid network", ep.ID)
		}
		if ep.Observer != "" && !obs[ep.Observer] {
			return fmt.Errorf("endpoint %s: unknown observer", ep.ID)
		}
		// via lists the edges and proxies a client reaches before the
		// endpoint's own node, in order; none may repeat or be that node.
		if len(ep.Via) > 8 {
			return fmt.Errorf("endpoint %s: at most 8 via hops", ep.ID)
		}
		for i, v := range ep.Via {
			if !nodes[v] || v == ep.Node || slices.Contains(ep.Via[:i], v) {
				return fmt.Errorf("endpoint %s: via needs distinct known nodes other than its own", ep.ID)
			}
		}
		ends[ep.ID] = ep
	}
	for _, l := range c.Links {
		if !ID(l.ID) || links[l.ID].ID != "" || !nodes[l.From] || !nodes[l.To] {
			return fmt.Errorf("invalid link %q", l.ID)
		}
		links[l.ID] = l
	}
	for _, p := range c.Paths {
		if !ID(p.ID) || paths[p.ID].ID != "" || !obs[p.Observer] {
			return fmt.Errorf("invalid path %q", p.ID)
		}
		paths[p.ID] = p
	}
	graph := map[string][]string{}
	for _, p := range c.Paths {
		for _, n := range p.Nodes {
			if !nodes[n] {
				return fmt.Errorf("path %s: missing node %q", p.ID, n)
			}
		}
		for i, id := range p.Links {
			l, ok := links[id]
			if !ok {
				return fmt.Errorf("path %s: missing link", p.ID)
			}
			if i > 0 && links[p.Links[i-1]].To != l.From {
				return fmt.Errorf("path %s: links are not contiguous", p.ID)
			}
		}
		if len(p.Candidates) > 0 && len(p.Proxies) > 0 {
			return fmt.Errorf("path %s: candidates and transport hops are distinct", p.ID)
		}
		for _, id := range append(append([]string{}, p.Candidates...), p.DependsOn...) {
			q, ok := paths[id]
			if !ok || q.Observer != p.Observer {
				return fmt.Errorf("path %s: missing/cross-observer dependency %q", p.ID, id)
			}
			graph[p.ID] = append(graph[p.ID], id)
		}
		if len(p.Proxies) > 12 {
			return fmt.Errorf("path %s: maximum 12 transport hops", p.ID)
		}
		for i, pv := range p.Proxies {
			if pv.Node != "" && !nodes[pv.Node] {
				return fmt.Errorf("path %s: unknown proxy node", p.ID)
			}
			if e := HostPort(pv.Address); e != nil {
				return fmt.Errorf("path %s: %w", p.ID, e)
			}
			switch pv.Kind {
			case "http-connect", "https-connect", "socks5":
			case "ssh":
				if i != 0 || pv.SSHKnownHosts == "" || pv.SSHIdentityFile == "" || pv.SSHUser == "" {
					return fmt.Errorf("path %s: SSH must be first, with identity, user and known_hosts", p.ID)
				}
				if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`).MatchString(pv.SSHUser) {
					return errors.New("unsafe SSH user")
				}
			default:
				return fmt.Errorf("unsupported proxy kind %q", pv.Kind)
			}
			if e := ProxyCredentials(pv, i); e != nil {
				return fmt.Errorf("path %s: %w", p.ID, e)
			}
			if len(pv.Username) > 255 {
				return errors.New("proxy username too long")
			}
		}
	}
	if e := acyclic(graph); e != nil {
		return fmt.Errorf("path %w", e)
	}
	graph = map[string][]string{}
	for _, n := range c.Nodes {
		if n.Parent != "" {
			graph[n.ID] = []string{n.Parent}
		}
	}
	if e := acyclic(graph); e != nil {
		return fmt.Errorf("node parent %w", e)
	}
	graph = map[string][]string{}
	for _, s := range c.Services {
		for _, id := range s.Endpoints {
			if _, ok := ends[id]; !ok {
				return fmt.Errorf("service %s: missing or foreign endpoint", s.ID)
			}
		}
		for _, d := range s.DependsOn {
			if !services[d] {
				return fmt.Errorf("service %s: missing dependency", s.ID)
			}
		}
		graph[s.ID] = s.DependsOn
	}
	if e := acyclic(graph); e != nil {
		return fmt.Errorf("service %w", e)
	}
	for _, x := range c.Checks {
		if e := unique(checks, x.ID, "check"); e != nil {
			return e
		}
		if !obs[x.Observer] {
			return fmt.Errorf("check %s: missing observer", x.ID)
		}
		if err := validateHost(x, c); err != nil {
			return fmt.Errorf("check %s: %w", x.ID, err)
		}
		if x.Endpoint != "" {
			ep, ok := ends[x.Endpoint]
			if !ok {
				return fmt.Errorf("check %s: missing endpoint", x.ID)
			}
			if ep.Observer != "" && ep.Observer != x.Observer {
				return fmt.Errorf("check %s: endpoint scope violation", x.ID)
			}
		}
		if x.Link != "" {
			if _, ok := links[x.Link]; !ok {
				return fmt.Errorf("check %s: missing link", x.ID)
			}
		}
		if x.Service != "" && !services[x.Service] {
			return fmt.Errorf("check %s: missing service", x.ID)
		}
		if x.Path != "" {
			p, ok := paths[x.Path]
			if !ok || p.Observer != x.Observer {
				return fmt.Errorf("check %s: missing/cross-observer path", x.ID)
			}
			if len(p.Candidates) > 0 {
				return fmt.Errorf("check %s: probe individual candidate or a named production-listener path, not an abstract policy", x.ID)
			}
		}
		if x.IntervalSeconds < 1 || x.IntervalSeconds > 86400 || x.TimeoutSeconds < 1 || x.TimeoutSeconds > MaxTimeoutSeconds || x.StaleSeconds < x.IntervalSeconds || x.StaleSeconds > 604800 || x.FailureThreshold < 1 || x.FailureThreshold > 20 || x.RecoveryThreshold < 1 || x.RecoveryThreshold > 20 {
			return fmt.Errorf("check %s: invalid timing/threshold", x.ID)
		}
		for _, ip := range []string{x.BindIP, x.ResolveIP} {
			if ip != "" {
				if _, e := netip.ParseAddr(ip); e != nil {
					return fmt.Errorf("check %s: invalid dial/bind IP", x.ID)
				}
			}
		}
		if (x.Kind == "tailscale" || x.Kind == "wireguard") && (x.BindIP != "" || x.ResolveIP != "" || x.Target != "") {
			return fmt.Errorf("check %s: local state adapters do not execute dial/bind/target overrides", x.ID)
		}
		if x.Kind == "icmp" && x.ResolveIP != "" {
			return fmt.Errorf("check %s: ICMP already requires a literal target; resolve_ip is unsupported", x.ID)
		}
		t := Target(c, x)
		switch x.Kind {
		case "http", "mihomo":
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
			if _, e := regexp.Compile(x.HTTP.BodyRegex); e != nil {
				return fmt.Errorf("check %s: invalid response regex", x.ID)
			}
			if e := HTTPHeaders(x.HTTP.RequestHeaders); e != nil {
				return fmt.Errorf("check %s: %w", x.ID, e)
			}
			for _, v := range x.HTTP.ExpectedExit {
				if _, e := netip.ParseAddr(v); e != nil {
					if _, e = netip.ParsePrefix(v); e != nil {
						return errors.New("expected_exit must contain IPs/CIDRs")
					}
				}
			}
			if x.HTTP.ExitFormat != "" && x.HTTP.ExitFormat != "plain" && x.HTTP.ExitFormat != "trace" && x.HTTP.ExitFormat != "json" {
				return errors.New("invalid exit_format")
			}
			if x.Kind == "mihomo" {
				if len(x.Adapter.ExpectedProviders) > 16 {
					return errors.New("maximum 16 expected providers per check")
				}
				for k := range x.Adapter.ExpectedProviders {
					if len(k) > 119 || secure.Text(k) != k {
						return errors.New("provider name exceeds evidence budget or contains control characters")
					}
				}
			}
		case "tcp", "tls", "ssh-banner", "dns", "udp-echo":
			if e := HostPort(t); e != nil {
				return fmt.Errorf("check %s: %w", x.ID, e)
			}
		case "icmp":
			if ip, e := netip.ParseAddr(t); e != nil || !ip.Is4() {
				return errors.New("ICMP currently requires an explicit IPv4 target")
			}
			if ip, _ := netip.ParseAddr(x.BindIP); x.BindIP != "" && !ip.Is4() {
				return fmt.Errorf("check %s: ICMP binds only an IPv4 address", x.ID)
			}
		case "tailscale":
			if x.Adapter.Peer == "" {
				return errors.New("tailscale check needs adapter.peer")
			}
		case "wireguard":
			if x.Adapter.Peer == "" || !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`).MatchString(x.Adapter.Interface) {
				return errors.New("wireguard needs interface and peer public key")
			}
		case "host":
			// Validated above; this check reads only the observer's host.
		default:
			return fmt.Errorf("check %s: unsupported kind %q", x.ID, x.Kind)
		}
		if x.Kind == "dns" {
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
		}
		if x.Path != "" && len(paths[x.Path].Proxies) > 0 {
			switch x.Kind {
			case "http", "tcp", "tls", "ssh-banner", "mihomo":
			default:
				return fmt.Errorf("check %s: this probe cannot traverse stream proxy chains", x.ID)
			}
		}
	}
	return nil
}
func acyclic(g map[string][]string) error {
	remaining := map[string]int{}
	parents := map[string][]string{}
	depth := map[string]int{}
	for id, deps := range g {
		remaining[id] = len(deps)
		for _, dep := range deps {
			parents[dep] = append(parents[dep], id)
			if _, ok := remaining[dep]; !ok {
				remaining[dep] = 0
			}
		}
	}
	queue := []string{}
	for id, n := range remaining {
		if n == 0 {
			queue = append(queue, id)
			depth[id] = 1
		}
	}
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		if depth[id] > 32 {
			return errors.New("reference depth exceeds 32")
		}
		for _, parent := range parents[id] {
			if depth[parent] < depth[id]+1 {
				depth[parent] = depth[id] + 1
			}
			remaining[parent]--
			if remaining[parent] == 0 {
				queue = append(queue, parent)
			}
		}
	}
	if len(queue) != len(remaining) {
		return errors.New("reference cycle")
	}
	return nil
}
func HostPort(s string) error {
	h, p, e := net.SplitHostPort(s)
	if e != nil || h == "" || strings.HasPrefix(h, "-") || strings.ContainsAny(h, "/\\") || strings.IndexFunc(h, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return errors.New("expected safe host:port")
	}
	n, e := net.LookupPort("tcp", p)
	if e != nil || n < 1 || n > 65535 {
		return errors.New("invalid port")
	}
	return nil
}

// hostHeader reports whether v can stand as a request's Host: a name or
// an address, with or without a port, and nothing more.
func hostHeader(v string) bool {
	u, e := url.Parse("http://" + v)
	return e == nil && u.Host == v && u.Hostname() != "" && !strings.HasPrefix(v, "-")
}
func Target(c model.Config, x model.Check) string {
	if x.Target != "" {
		value, _ := expandAddress(c.Addresses, x.Target)
		return value
	}
	for _, e := range c.Endpoints {
		if e.ID == x.Endpoint {
			value, _ := expandAddress(c.Addresses, e.Address)
			return value
		}
	}
	return ""
}
func Check(c model.Config, id string) (model.Check, bool) {
	for _, x := range c.Checks {
		if x.ID == id {
			return x, true
		}
	}
	return model.Check{}, false
}
func Path(c model.Config, id string) model.Path {
	for _, p := range c.Paths {
		if p.ID == id {
			resolved, _ := resolvePath(c, p)
			return resolved
		}
	}
	return model.Path{}
}
func Revision(c model.Config) string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// PublicURL is s as viewers may see it: without user information or a
// fragment, and with any query replaced by "redacted". An address that is
// not a URL, such as host:port, loses the same parts: what precedes an @,
// what follows a ? and what follows a #.
func PublicURL(s string) string {
	s, _, _ = strings.Cut(s, "#")
	if u, e := url.Parse(s); e == nil && u.Scheme != "" && u.Opaque == "" {
		u.User = nil
		if u.RawQuery != "" {
			u.RawQuery = "redacted"
		}
		return u.String()
	}
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if before, _, ok := strings.Cut(s, "?"); ok {
		s = before + "?redacted"
	}
	return s
}

// Public returns a deep copy; redaction never modifies executor configuration.
func Public(c model.Config) model.Config {
	resolved, err := ResolveAddresses(c)
	if err != nil {
		return model.Config{Schema: c.Schema, Project: c.Project}
	}
	out := Clone(resolved)
	for i := range out.Endpoints {
		out.Endpoints[i].Address = PublicURL(out.Endpoints[i].Address)
	}
	for i := range out.Checks {
		x := &out.Checks[i]
		x.Target = PublicURL(x.Target)
		x.HTTP.BearerEnv = ""
		x.Adapter.TokenEnv = ""
		x.HTTP.RequestHeaders = nil
		x.CAFile = ""
	}
	for i := range out.Paths {
		for j := range out.Paths[i].Proxies {
			p := &out.Paths[i].Proxies[j]
			p.Username = ""
			p.PasswordEnv = ""
			p.SSHUser = ""
			p.SSHIdentityFile = ""
			p.SSHKnownHosts = ""
			p.CAFile = ""
		}
	}
	return out
}

// StorageWeights returns the trusted allocation, independently of check contracts.
func StorageWeights(c model.Config) map[string]int {
	w := make(map[string]int, len(c.Observers))
	for _, o := range c.Observers {
		w[o.ID] = max(o.StorageWeight, 1)
	}
	return w
}
