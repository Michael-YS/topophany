package config_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func dnsTransportConfig(transport, target string) model.Config {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind = "dns"
	x.Target = target
	x.DNS = model.DNSOptions{Name: "test.example", Type: "A", Transport: transport}
	return c
}

// The transport selects the target rule: udp, tcp and dot take host:port
// (dot usually 853, but any port), doh takes an https URL without userinfo,
// query or fragment, and anything else is refused instead of run as UDP.
func TestDNSTransportValidation(t *testing.T) {
	for _, tc := range []struct {
		name, transport, target string
		valid                   bool
	}{
		{"unset udp", "", "127.0.0.1:53", true},
		{"udp", "udp", "127.0.0.1:53", true},
		{"tcp", "tcp", "127.0.0.1:53", true},
		{"dot 853", "dot", "1.1.1.1:853", true},
		{"dot any port", "dot", "1.1.1.1:8853", true},
		{"doh", "doh", "https://cloudflare-dns.com/dns-query", true},
		{"doh no path", "doh", "https://cloudflare-dns.com", true},
		{"doh port", "doh", "https://1.1.1.1:443/dns-query", true},
		{"doh plain http", "doh", "http://cloudflare-dns.com/dns-query", false},
		{"doh userinfo", "doh", "https://user@cloudflare-dns.com/dns-query", false},
		{"doh query", "doh", "https://cloudflare-dns.com/dns-query?dns=AA", false},
		{"doh fragment", "doh", "https://cloudflare-dns.com/dns-query#answer", false},
		{"doh without scheme", "doh", "cloudflare-dns.com:443", false},
		{"doh with host:port only", "doh", "1.1.1.1:853", false},
		{"udp with URL", "udp", "https://cloudflare-dns.com/dns-query", false},
		{"dot with URL", "dot", "https://cloudflare-dns.com/dns-query", false},
		{"tcp with host only", "tcp", "1.1.1.1", false},
		{"unknown transport", "quic", "127.0.0.1:53", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dnsTransportConfig(tc.transport, tc.target)
			if err := config.Validate(c); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
	c := dnsTransportConfig("quic", "127.0.0.1:53")
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("unknown transport error is %v", err)
	}
}

// dns.transport is inside DNSOptions, so it is part of the measurement
// contract: unset it writes nothing and leaves every contract hash
// unchanged, and set it gives that check a new contract. It is not a secret,
// so redaction keeps it.
func TestDNSTransportContractCloneAndJSON(t *testing.T) {
	c := dnsTransportConfig("", "127.0.0.1:853")
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	before := config.Contracts(c)
	config.Defaults(&c)
	dnsJSON, err := json.Marshal(c.Checks[0].DNS)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dnsJSON), `"transport"`) {
		t.Fatal("unset DNS transport was written")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back model.Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(config.Contracts(back), before) {
		t.Fatal("unset DNS transport changed a contract")
	}

	declared := dnsTransportConfig("dot", "127.0.0.1:853")
	if config.Contracts(declared)[declared.Checks[0].ID] == before[declared.Checks[0].ID] {
		t.Fatal("DNS transport did not change the check's contract")
	}
	if err := config.Validate(declared); err != nil {
		t.Fatalf("valid dot transport refused: %v", err)
	}
	if got := config.Public(declared).Checks[0].DNS.Transport; got != "dot" {
		t.Fatalf("public topology changed dns.transport to %q", got)
	}
	clone := config.Clone(declared)
	clone.Checks[0].DNS.Transport = "doh"
	if declared.Checks[0].DNS.Transport != "dot" {
		t.Fatal("clone shares the DNS transport")
	}
}

func TestExampleTopologiesCarryNoDNSTransport(t *testing.T) {
	for path, c := range exampleTopologies(t) {
		for _, x := range c.Checks {
			if x.DNS.Transport != "" {
				t.Fatalf("%s: check %s carries a DNS transport", path, x.ID)
			}
		}
		before := config.Contracts(c)
		out, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var back model.Config
		if err := json.Unmarshal(out, &back); err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(before, config.Contracts(back)) {
			t.Fatalf("%s: unset DNS transport changed a contract", path)
		}
	}
}
