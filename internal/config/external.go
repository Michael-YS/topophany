package config

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Sources and external bindings are validated here, before execution, so an
// invalid topology cannot be made to look like a collection failure. Adapter
// packages retain endpoint/version/field parsing responsibility.
func defaultSources(c *model.Config) {
	for i := range c.Sources {
		s := &c.Sources[i]
		if s.IntervalSeconds == 0 {
			s.IntervalSeconds = 60
		}
		if s.TimeoutSeconds == 0 {
			s.TimeoutSeconds = 8
		}
		if s.StaleSeconds == 0 {
			s.StaleSeconds = s.IntervalSeconds * 3
		}
		if s.MaxResponseBytes == 0 {
			s.MaxResponseBytes = 1 << 20
		}
		if s.MaxPages == 0 {
			s.MaxPages = 1
		}
		if s.MaxObjects == 0 {
			s.MaxObjects = model.MaxSourceObjects
		}
		if s.MaxReportBytes == 0 {
			s.MaxReportBytes = model.MaxReportBytes
		}
	}
}

// sourceSchedule copies the source-owned schedule onto an external check. An
// omitted value therefore resolves to the source's; an explicit different
// value is refused in validation rather than silently overridden.
func sourceSchedule(c model.Config, x *model.Check) {
	if x.External == nil {
		return
	}
	for _, s := range c.Sources {
		if s.ID != x.External.Source {
			continue
		}
		if x.IntervalSeconds == 0 {
			x.IntervalSeconds = s.IntervalSeconds
		}
		if x.TimeoutSeconds == 0 {
			x.TimeoutSeconds = s.TimeoutSeconds
		}
		if x.StaleSeconds == 0 {
			x.StaleSeconds = s.StaleSeconds
		}
		return
	}
}

func validatesSourceAdapter(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return !(s[0] >= '0' && s[0] <= '9') && s[0] != '.' && s[0] != '-' && s[0] != '_'
}

func sourceObjectID(s string, limit int) bool {
	if len(s) < 1 || len(s) > limit || secure.Text(s) != s || strings.TrimSpace(s) != s {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:/-", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func validateSources(c model.Config) (map[string]model.Source, error) {
	if len(c.Sources) > model.MaxSources {
		return nil, fmt.Errorf("at most %d sources are supported", model.MaxSources)
	}
	observers := map[string]bool{}
	for _, o := range c.Observers {
		observers[o.ID] = true
	}
	paths := map[string]model.Path{}
	for _, p := range c.Paths {
		paths[p.ID] = p
	}
	out := make(map[string]model.Source, len(c.Sources))
	for _, s := range c.Sources {
		if !ID(s.ID) || out[s.ID].ID != "" {
			return nil, fmt.Errorf("invalid or duplicate source ID %q", s.ID)
		}
		if s.Adapter == "" || !validatesSourceAdapter(s.Adapter) {
			return nil, fmt.Errorf("source %s: adapter must be a lowercase adapter type", s.ID)
		}
		if !observers[s.Observer] {
			return nil, fmt.Errorf("source %s: missing observer", s.ID)
		}
		if s.Target == "" || len(s.Target) > model.MaxSourceTarget || secure.Text(s.Target) != s.Target || strings.TrimSpace(s.Target) != s.Target {
			return nil, fmt.Errorf("source %s: target must be a bounded, safe address", s.ID)
		}
		if u, e := url.Parse(s.Target); e == nil && (u.User != nil || u.Fragment != "") {
			return nil, fmt.Errorf("source %s: target cannot carry userinfo or a fragment", s.ID)
		}
		if s.CredentialEnv != "" && !SecretEnvName(s.CredentialEnv) {
			return nil, fmt.Errorf("source %s: invalid credential environment variable name", s.ID)
		}
		if s.Path != "" {
			p, ok := paths[s.Path]
			if !ok || p.Observer != s.Observer {
				return nil, fmt.Errorf("source %s: path must exist for the source observer", s.ID)
			}
		}
		if s.IntervalSeconds < 1 || s.IntervalSeconds > 86400 || s.TimeoutSeconds < 1 || s.TimeoutSeconds > MaxTimeoutSeconds || s.StaleSeconds < s.IntervalSeconds || s.StaleSeconds > 604800 {
			return nil, fmt.Errorf("source %s: invalid timing", s.ID)
		}
		if s.MaxResponseBytes < 1024 || s.MaxResponseBytes > 8<<20 {
			return nil, fmt.Errorf("source %s: max_response_bytes must be 1024..8388608", s.ID)
		}
		if s.MaxPages < 1 || s.MaxPages > model.MaxSourcePages {
			return nil, fmt.Errorf("source %s: max_pages must be 1..%d", s.ID, model.MaxSourcePages)
		}
		if s.MaxObjects < 1 || s.MaxObjects > model.MaxSourceObjects {
			return nil, fmt.Errorf("source %s: max_objects must be 1..%d", s.ID, model.MaxSourceObjects)
		}
		if s.MaxReportBytes < 1024 || s.MaxReportBytes > model.MaxReportBytes {
			return nil, fmt.Errorf("source %s: max_report_bytes must be 1024..%d", s.ID, model.MaxReportBytes)
		}
		for _, ip := range []string{s.BindIP, s.ResolveIP} {
			if ip != "" {
				if _, e := netip.ParseAddr(ip); e != nil {
					return nil, fmt.Errorf("source %s: invalid dial/bind IP", s.ID)
				}
			}
		}
		if (s.CredentialEnv != "" || sourceTargetQuery(s.Target)) && !sourceTargetSecure(c, s) {
			return nil, fmt.Errorf("source %s: credentials and target queries require HTTPS or direct literal loopback without a proxy", s.ID)
		}
		if s.CAFile != "" && (len(s.CAFile) > 1024 || secure.Text(s.CAFile) != s.CAFile) {
			return nil, fmt.Errorf("source %s: ca_file must be a bounded safe path", s.ID)
		}
		if len(s.Name) > 128 || secure.Text(s.Name) != s.Name || len(s.Notes) > 4096 || secure.Text(s.Notes) != s.Notes {
			return nil, fmt.Errorf("source %s: name or notes exceed their presentation budget", s.ID)
		}
		out[s.ID] = s
	}
	return out, nil
}

func sourceTargetQuery(target string) bool {
	u, err := url.Parse(target)
	return err == nil && u.RawQuery != ""
}

func sourceTargetSecure(c model.Config, s model.Source) bool {
	u, err := url.Parse(s.Target)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" || !literalLoopback(u.Hostname()) {
		return false
	}
	host := u.Hostname()
	if s.ResolveIP != "" {
		host = s.ResolveIP
	}
	if !literalLoopback(host) || s.Path != "" {
		return false
	}
	return true
}

func validateExternal(x model.Check, c model.Config, sources map[string]model.Source) error {
	if x.Kind != "external" {
		if x.External != nil {
			return errors.New("external binding requires kind external")
		}
		return nil
	}
	b := x.External
	if b == nil {
		return errors.New("external checks require an external binding")
	}
	if x.Endpoint != "" || x.Target != "" || x.Path != "" || x.Link != "" || x.BindIP != "" || x.ResolveIP != "" || x.ServerName != "" || x.CAFile != "" {
		return errors.New("external check transport comes from its source; endpoint, target, path, link and dial/TLS overrides are not allowed")
	}
	if x.HTTP.BearerEnv != "" || x.HTTP.MaxBodyBytes > 0 || x.HTTP.Method != "" || len(x.HTTP.Status) > 0 || x.HTTP.Contains != "" || x.HTTP.BodyRegex != "" || len(x.HTTP.Headers) > 0 || len(x.HTTP.RequestHeaders) > 0 || len(x.HTTP.RequestHeaderEnvs) > 0 || len(x.HTTP.ExpectedExit) > 0 || len(x.HTTP.ExpectedPops) > 0 || x.HTTP.ExitFormat != "" || x.HTTP.ExitJSONKey != "" || x.HTTP.ExpectedTrace != nil {
		return errors.New("external checks cannot carry HTTP options")
	}
	if x.DNS.Name != "" || x.DNS.Type != "" || len(x.DNS.ExpectedIPs) > 0 || x.DNS.Transport != "" {
		return errors.New("external checks cannot carry DNS options")
	}
	if x.Adapter.TokenEnv != "" || x.Adapter.RootProxy != "" || x.Adapter.ExpectedMode != "" || len(x.Adapter.ExpectedProviders) > 0 || x.Adapter.Peer != "" || x.Adapter.Interface != "" || x.Adapter.ProviderReference != nil || len(x.Adapter.ExpectedAllowedIPs) > 0 || x.Adapter.ExpectedKeepaliveSeconds != nil || x.Adapter.ExpectedTUN != nil || len(x.Adapter.ProviderMatchModes) > 0 || x.Adapter.TailscaleDiagnostic != "" || x.Adapter.Cloudflared != nil || x.Adapter.Cloudflare != nil {
		return errors.New("external checks cannot carry adapter options")
	}
	if x.Host != nil {
		return errors.New("external checks cannot carry host options")
	}
	s, ok := sources[b.Source]
	if !ok {
		return fmt.Errorf("external check references missing source %q", b.Source)
	}
	if x.Observer != s.Observer {
		return errors.New("external check observer must equal its source observer")
	}
	if x.IntervalSeconds != s.IntervalSeconds || x.TimeoutSeconds != s.TimeoutSeconds || x.StaleSeconds != s.StaleSeconds {
		return errors.New("external check schedule and timeout come from its source and must match")
	}
	if !sourceObjectID(b.Object, 256) {
		return errors.New("external object must be a bounded stable upstream object ID")
	}
	switch b.Report {
	case model.ReportTypeResource:
		if b.PrimaryDisk != "" && !sourceObjectID(b.PrimaryDisk, 128) {
			return errors.New("external primary_disk must be a bounded stable disk ID")
		}
	case model.ReportTypeStatus:
		if b.PrimaryDisk != "" || b.Assertions != nil {
			return errors.New("status bindings cannot select a disk or declare resource assertions")
		}
	default:
		return errors.New("external report must be resource or status")
	}
	if b.MaxAgeSeconds < 1 || b.MaxAgeSeconds > 604800 {
		return errors.New("external max_age_seconds must be 1..604800")
	}
	node := false
	for _, n := range c.Nodes {
		node = node || n.ID == b.Node
	}
	if !node {
		return fmt.Errorf("external binding references missing node %q", b.Node)
	}
	if x.Service != "" {
		for _, sv := range c.Services {
			if sv.ID == x.Service {
				if sv.Node != b.Node {
					return errors.New("external service must run on the binding's node")
				}
				break
			}
		}
	}
	if a := b.Assertions; a != nil {
		for _, v := range []*float64{a.CPUPercentMax, a.MemoryPercentMax, a.DiskPercentMax} {
			if v == nil {
				continue
			}
			if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100 {
				return errors.New("external resource thresholds must be 0..100")
			}
		}
		if a.DiskPercentMax != nil && b.PrimaryDisk == "" {
			return errors.New("a disk threshold requires primary_disk")
		}
	}
	return nil
}

// validateResourceSelections refuses new multi-source ambiguity while leaving
// every native-only topology's existing card selection untouched.
func validateResourceSelections(c model.Config) error {
	type candidate struct {
		check string
	}
	providers := map[string][]candidate{}
	external := map[string]bool{}
	for _, x := range c.Checks {
		if x.Kind == "external" && x.External != nil && x.External.Report == model.ReportTypeResource {
			providers[x.External.Node] = append(providers[x.External.Node], candidate{x.ID})
			external[x.External.Node] = true
			continue
		}
		if x.Kind == "host" {
			for _, o := range c.Observers {
				if o.ID == x.Observer && o.Node != "" {
					providers[o.Node] = append(providers[o.Node], candidate{x.ID})
					break
				}
			}
		}
	}
	checkNode := map[string]string{}
	for _, x := range c.Checks {
		if x.Kind == "external" && x.External != nil {
			checkNode[x.ID] = x.External.Node
		}
	}
	for _, n := range c.Nodes {
		if len(n.ResourceChecks) == 0 {
			if external[n.ID] && len(providers[n.ID]) > 1 {
				return fmt.Errorf("node %s: multiple resource sources need an ordered resource_checks list", n.ID)
			}
			continue
		}
		if len(n.ResourceChecks) > 16 {
			return fmt.Errorf("node %s: at most 16 resource checks", n.ID)
		}
		seen := map[string]bool{}
		for _, id := range n.ResourceChecks {
			if seen[id] || !slices.ContainsFunc(providers[n.ID], func(c candidate) bool { return c.check == id }) {
				return fmt.Errorf("node %s: resource check %q does not provide resources for this node", n.ID, id)
			}
			seen[id] = true
		}
	}
	for node, id := range checkNode {
		if len(providers[node]) > 0 && len(c.Nodes) > 0 {
			found := false
			for _, n := range c.Nodes {
				if n.ID == node {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("external resource %s references missing node", id)
			}
		}
	}
	return nil
}

// Source returns an ID's source, if it is declared.
func Source(c model.Config, id string) (model.Source, bool) {
	for _, s := range c.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return model.Source{}, false
}

// ExternalChecksForSource returns the external checks bound to source, in
// topology order. The runner uses it to fan one collection out.
func ExternalChecksForSource(c model.Config, source string) []model.Check {
	out := []model.Check{}
	for _, x := range c.Checks {
		if x.Kind == "external" && x.External != nil && x.External.Source == source {
			out = append(out, x)
		}
	}
	return out
}
