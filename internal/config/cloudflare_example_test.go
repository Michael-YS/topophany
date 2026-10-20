package config_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// The Cloudflare worked example is a documentation topology, not a credential
// file: every check, link, endpoint, service and observer it names resolves,
// its active checks carry no secret and no adapters that would need a token,
// and the fields added for Cloudflare are shown rather than only described.
func TestCloudflareExampleResolvesItsChecks(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "cloudflare.json")
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}

	nodes := map[string]bool{}
	for _, n := range c.Nodes {
		nodes[n.ID] = true
	}
	observers := map[string]bool{}
	for _, o := range c.Observers {
		observers[o.ID] = true
	}
	endpoints := map[string]model.Endpoint{}
	for _, e := range c.Endpoints {
		if !nodes[e.Node] {
			t.Fatalf("endpoint %s: missing node %q", e.ID, e.Node)
		}
		for _, via := range e.Via {
			if !nodes[via] {
				t.Fatalf("endpoint %s: missing via node %q", e.ID, via)
			}
		}
		endpoints[e.ID] = e
	}
	links := map[string]model.Link{}
	for _, l := range c.Links {
		if !nodes[l.From] || !nodes[l.To] {
			t.Fatalf("link %s does not resolve both ends", l.ID)
		}
		links[l.ID] = l
	}
	services := map[string]model.Service{}
	for _, s := range c.Services {
		if !nodes[s.Node] {
			t.Fatalf("service %s: missing node", s.ID)
		}
		for _, id := range s.Endpoints {
			if _, ok := endpoints[id]; !ok {
				t.Fatalf("service %s: missing endpoint %q", s.ID, id)
			}
		}
		services[s.ID] = s
	}
	paths := map[string]model.Path{}
	for _, p := range c.Paths {
		paths[p.ID] = p
	}
	checks := map[string]model.Check{}
	for _, x := range c.Checks {
		checks[x.ID] = x
		if !observers[x.Observer] {
			t.Fatalf("check %s: missing observer", x.ID)
		}
		if x.Endpoint != "" {
			ep, ok := endpoints[x.Endpoint]
			if !ok {
				t.Fatalf("check %s: missing endpoint %q", x.ID, x.Endpoint)
			}
			if ep.Observer != "" && ep.Observer != x.Observer {
				t.Fatalf("check %s runs outside its endpoint's observer scope", x.ID)
			}
		}
		if x.Link != "" {
			if _, ok := links[x.Link]; !ok {
				t.Fatalf("check %s: missing link %q", x.ID, x.Link)
			}
		}
		if x.Service != "" {
			if _, ok := services[x.Service]; !ok {
				t.Fatalf("check %s: missing service %q", x.ID, x.Service)
			}
		}
		if x.Path != "" {
			p, ok := paths[x.Path]
			if !ok || p.Observer != x.Observer {
				t.Fatalf("check %s: missing or foreign path %q", x.ID, x.Path)
			}
		}
		if x.Kind == "cloudflare" {
			t.Fatalf("check %s runs the API read the example only documents", x.ID)
		}
		if x.HTTP.BearerEnv != "" || len(x.HTTP.RequestHeaders) > 0 || x.Adapter.TokenEnv != "" {
			t.Fatalf("check %s carries a credential value", x.ID)
		}
	}

	// A proxied origin with its own direct check: the pinned dial address
	// keeps Host and SNI as the zone name and never asks the edge.
	direct := checks["site-origin-direct"]
	if direct.ResolveIP != "203.0.113.10" || direct.Endpoint != "site-origin" || len(endpoints["site-origin"].Via) != 0 {
		t.Fatalf("the direct origin check is not direct: %+v", direct)
	}
	if len(endpoints["site-public"].Via) != 1 || endpoints["site-public"].Via[0] != "cf" {
		t.Fatalf("the public origin endpoint is not fronted by Cloudflare: %+v", endpoints["site-public"])
	}

	// The edge check declares the colos this deployment expects.
	if got := checks["site-via-edge-home"].HTTP.ExpectedPops; !slices.Equal(got, []string{"HKG", "NRT", "SIN"}) {
		t.Fatalf("expected_pops = %v", got)
	}

	// A trace check per observer, each requiring exit_format trace.
	traced := 0
	for _, x := range c.Checks {
		if x.HTTP.ExpectedTrace == nil {
			continue
		}
		traced++
		if x.HTTP.ExitFormat != "trace" || x.Target != "https://www.cloudflare.com/cdn-cgi/trace" {
			t.Fatalf("trace check %s: %+v", x.ID, x.HTTP)
		}
	}
	if traced != 2 || checks["trace-home"].Observer != "obs-home" || checks["trace-origin"].Observer != "obs-origin" {
		t.Fatalf("want a trace check per observer, got %d", traced)
	}

	// DNS over TLS names the certificate the IP target does not carry.
	dot := checks["dot-home"]
	if dot.DNS.Transport != "dot" || dot.ServerName != "one.one.one.one" || dot.DNS.Name != "www.example.com" {
		t.Fatalf("DoT check: %+v", dot)
	}

	// The tunnel: a connector check on the link, a loopback app check beside
	// the public check through the edge.
	connector := checks["tunnel-connector"]
	if connector.Kind != "cloudflared" || connector.Link != "tunnel" || connector.Target != "http://127.0.0.1:20241" {
		t.Fatalf("connector check: %+v", connector)
	}
	if options := connector.Adapter.CloudflaredOptions(); options.ExpectedConnections == nil || *options.ExpectedConnections != 4 {
		t.Fatalf("connector options: %+v", options)
	}
	if checks["home-local"].Endpoint != "home-local" || endpoints["home-local"].Network != "loopback" ||
		checks["home-via-edge"].Endpoint != "home-public" || len(endpoints["home-public"].Via) != 1 {
		t.Fatal("the tunneled service is not measured both locally and through the edge")
	}
	if links["tunnel"].Transport != "cloudflared" || links["tunnel"].From != "homebox" || links["tunnel"].To != "cf" {
		t.Fatalf("tunnel link: %+v", links["tunnel"])
	}

	// The Access hostname sends a service token from the environment: header
	// names and variable names only, never values.
	envs := checks["access-home"].HTTP.RequestHeaderEnvs
	if len(envs) != 2 || envs["CF-Access-Client-Id"] != "CF_ACCESS_CLIENT_ID" || envs["CF-Access-Client-Secret"] != "CF_ACCESS_CLIENT_SECRET" {
		t.Fatalf("Access header variables: %v", envs)
	}

	// The three API reads are named in the edge's notes with their token
	// variable, not configured as checks.
	notes := ""
	for _, n := range c.Nodes {
		if n.ID == "cf" {
			notes = n.Notes
		}
	}
	for _, want := range []string{"CF_API_TOKEN", `"tunnel"`, `"dns_record"`, `"pool"`} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the edge notes do not name %s: %q", want, notes)
		}
	}

	// Every active address is documentation or reserved, except the public
	// resolver the DNS over TLS check asks.
	for _, e := range c.Endpoints {
		if !strings.Contains(e.Address, ".example.com") && !strings.HasPrefix(e.Address, "http://127.0.0.1") && !strings.HasPrefix(e.Address, "1.1.1.1:") {
			t.Fatalf("endpoint %s uses a real address: %s", e.ID, e.Address)
		}
	}
}
