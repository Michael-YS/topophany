package config_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func cloudflaredConfig(target string) model.Config {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind, x.Target, x.Endpoint, x.Path, x.Service = "cloudflared", target, "", "", ""
	x.HTTP = model.HTTPOptions{}
	return c
}

func TestCloudflaredTargetValidation(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		ca           string
		valid        bool
	}{
		{"loopback", "http://127.0.0.1:20241", "", true},
		{"ipv6 loopback", "http://[::1]:20241", "", true},
		{"trailing slash", "http://127.0.0.1:20241/", "", true},
		{"https with ca", "https://127.0.0.1:20241", "/tmp/ca.pem", true},
		{"https without ca", "https://127.0.0.1:20241", "", false},
		{"localhost", "http://localhost:20241", "", false},
		{"remote", "http://192.0.2.1:20241", "", false},
		{"host name", "http://metrics.internal:20241", "", false},
		{"userinfo", "http://user@127.0.0.1:20241", "", false},
		{"query", "http://127.0.0.1:20241/?token=x", "", false},
		{"fragment", "http://127.0.0.1:20241/#f", "", false},
		{"path", "http://127.0.0.1:20241/metrics", "", false},
		{"no port", "http://127.0.0.1", "", false},
		{"no value", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudflaredConfig(tc.target)
			c.Checks[0].CAFile = tc.ca
			if err := config.Validate(c); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
}

// The listener is a local process: the adapter refuses a path, a source
// binding, a dial override and any proxy chain, and it is not in the
// proxy-traversal kind list. Each override is refused for its own reason: a
// path of any kind is unsupported before the proxy-traversal rule can apply
// to it, so a proxy chain is never the reason this kind gives.
func TestCloudflaredRefusesRoutingOverrides(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.Config, *model.Check)
		want   string
	}{
		{"bind_ip", func(_ *model.Config, x *model.Check) { x.BindIP = "127.0.0.1" }, "does not accept bind_ip or resolve_ip"},
		{"resolve_ip", func(_ *model.Config, x *model.Check) { x.ResolveIP = "127.0.0.1" }, "does not accept bind_ip or resolve_ip"},
		{"path without proxies", func(_ *model.Config, x *model.Check) { x.Path = "direct" }, "path is unsupported"},
		{"path with proxies", func(c *model.Config, x *model.Check) {
			c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:1080"}}
			x.Path = "direct"
		}, "path is unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudflaredConfig("http://127.0.0.1:20241")
			tc.mutate(&c, &c.Checks[0])
			err := config.Validate(c)
			if err == nil {
				t.Fatal("override accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refused for the wrong reason: %v; want %q", err, tc.want)
			}
		})
	}
}

func TestCloudflaredOptionsValidation(t *testing.T) {
	intp := func(v int) *int { return &v }
	for _, tc := range []struct {
		name    string
		options *model.CloudflaredOptions
		valid   bool
	}{
		{"unset", nil, true},
		{"empty", &model.CloudflaredOptions{}, true},
		{"connections", &model.CloudflaredOptions{ExpectedConnections: intp(4)}, true},
		{"one connection", &model.CloudflaredOptions{ExpectedConnections: intp(1)}, true},
		{"eight connections", &model.CloudflaredOptions{ExpectedConnections: intp(8)}, true},
		{"zero connections", &model.CloudflaredOptions{ExpectedConnections: intp(0)}, false},
		{"nine connections", &model.CloudflaredOptions{ExpectedConnections: intp(9)}, false},
		{"uppercase colo", &model.CloudflaredOptions{ExpectedColos: []string{"HKG"}}, true},
		{"lowercase colo", &model.CloudflaredOptions{ExpectedColos: []string{"hkg"}}, true},
		{"eight colos", &model.CloudflaredOptions{ExpectedColos: []string{"hkg", "nrt", "lax", "sfo", "sea", "ord", "iad", "jfk"}}, true},
		{"nine colos", &model.CloudflaredOptions{ExpectedColos: []string{"hkg", "nrt", "lax", "sfo", "sea", "ord", "iad", "jfk", "sin"}}, false},
		{"two letters", &model.CloudflaredOptions{ExpectedColos: []string{"hk"}}, false},
		{"duplicate case", &model.CloudflaredOptions{ExpectedColos: []string{"HKG", "hkg"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudflaredConfig("http://127.0.0.1:20241")
			c.Checks[0].Adapter.Cloudflared = tc.options
			if err := config.Validate(c); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
	c := cloudflaredConfig("http://127.0.0.1:20241")
	bad := 0
	c.Checks[0].Adapter.Cloudflared = &model.CloudflaredOptions{ExpectedConnections: &bad}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "expected_connections must be 1..8") {
		t.Fatalf("error is %v", err)
	}
}

// The nested options are part of the measurement contract: unset writes
// nothing and leaves a default configuration's contracts unchanged; set, they
// change that check's contract. They are not secrets, so public redaction
// keeps them, and Clone detaches the pointer and the slice.
func TestCloudflaredContractCloneAndJSON(t *testing.T) {
	c := cloudflaredConfig("http://127.0.0.1:20241")
	before := config.Contracts(c)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"cloudflared":{`) {
		t.Fatal("default config carries cloudflared options")
	}
	if config.Validate(c) != nil {
		t.Fatal("valid cloudflared check refused")
	}
	base := config.Contracts(c)[c.Checks[0].ID]

	declared := cloudflaredConfig("http://127.0.0.1:20241")
	expected := 4
	declared.Checks[0].Adapter.Cloudflared = &model.CloudflaredOptions{ExpectedConnections: &expected, ExpectedColos: []string{"HKG", "NRT"}}
	if config.Contracts(declared)[declared.Checks[0].ID] == base {
		t.Fatal("cloudflared options did not change the contract")
	}
	if !mapsEqual(before, config.Contracts(declared)) {
		// Only the check's own contract may move; every other check keeps
		// its hash.
		for id, hash := range before {
			if id != declared.Checks[0].ID && config.Contracts(declared)[id] != hash {
				t.Fatalf("%s contract changed", id)
			}
		}
	}
	if public := config.Public(declared).Checks[0].Adapter.Cloudflared; public == nil || public.ExpectedConnections == nil || *public.ExpectedConnections != 4 || len(public.ExpectedColos) != 2 {
		t.Fatalf("public topology lost cloudflared options: %+v", public)
	}
	copy := config.Clone(declared)
	*copy.Checks[0].Adapter.Cloudflared.ExpectedConnections = 2
	copy.Checks[0].Adapter.Cloudflared.ExpectedColos[0] = "LAX"
	if *declared.Checks[0].Adapter.Cloudflared.ExpectedConnections != 4 || declared.Checks[0].Adapter.Cloudflared.ExpectedColos[0] != "HKG" {
		t.Fatal("clone aliases cloudflared options")
	}
	view := declared.Checks[0].Adapter.CloudflaredOptions()
	if view.ExpectedConnections == nil || *view.ExpectedConnections != 4 || len(view.ExpectedColos) != 2 {
		t.Fatal("typed view lost options")
	}
	// The view owns both mutable values.
	*view.ExpectedConnections, view.ExpectedColos[0] = 2, "LAX"
	if *declared.Checks[0].Adapter.Cloudflared.ExpectedConnections != 4 || declared.Checks[0].Adapter.Cloudflared.ExpectedColos[0] != "HKG" {
		t.Fatal("typed view aliases the adapter")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Public redaction clears every ca_file, but a cloudflared listener read over
// https needs one to validate. ValidatePublic restores a non-secret
// placeholder on its owned copy so a public snapshot is accepted, and the real
// path never appears in the public topology.
func TestCloudflaredPublicHTTPSValidates(t *testing.T) {
	const secretCA = "/private/ca/cloudflared.pem"
	for _, tc := range []struct {
		name   string
		mutate func(*model.Config, *model.Check)
	}{
		{"direct target", func(_ *model.Config, x *model.Check) {
			x.Target, x.CAFile = "https://127.0.0.1:20241", secretCA
		}},
		{"endpoint target", func(c *model.Config, x *model.Check) {
			c.Endpoints = append(c.Endpoints, model.Endpoint{ID: "cf-metrics", Node: "home", Address: "https://127.0.0.1:20241", Network: "loopback", Observer: "home"})
			x.Target, x.Endpoint, x.CAFile = "", "cf-metrics", secretCA
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudflaredConfig("http://127.0.0.1:20241")
			tc.mutate(&c, &c.Checks[0])
			if err := config.Validate(c); err != nil {
				t.Fatalf("the executor topology does not validate: %v", err)
			}
			public := config.Public(c)
			raw, err := json.Marshal(public)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), secretCA) {
				t.Fatal("public topology carries the real ca_file")
			}
			if err := config.ValidatePublic(public); err != nil {
				t.Fatalf("public snapshot of an https listener was refused: %v", err)
			}
		})
	}
}

// Every topology shipped in examples/ except the Cloudflare worked example was
// written before adapter.cloudflared, so none of those may carry it, not even
// as a null. Unset, the field writes nothing and leaves every contract hash
// unchanged through a JSON round trip; the worked example opts in and is
// checked by TestCloudflareExampleResolvesItsChecks instead.
func TestExampleTopologiesCarryNoCloudflared(t *testing.T) {
	for path, c := range exampleTopologies(t) {
		if optedInExample(path) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if containsKey(document, "cloudflared") {
			t.Fatalf("%s carries adapter.cloudflared", path)
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
		if !mapsEqual(before, config.Contracts(back)) {
			t.Fatalf("%s: unset cloudflared changed a contract", path)
		}
	}
}

func containsKey(value any, key string) bool {
	switch v := value.(type) {
	case map[string]any:
		if _, ok := v[key]; ok {
			return true
		}
		for _, child := range v {
			if containsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsKey(child, key) {
				return true
			}
		}
	}
	return false
}
