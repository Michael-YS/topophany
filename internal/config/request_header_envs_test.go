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

// A secret request header names an environment variable, never its value.
// Header names follow request_headers; Authorization and Cookie keep their
// prohibition, a name may not be in both maps whatever its case, and a
// variable name follows the same bounded rule as bearer_env.
func TestRequestHeaderEnvValidation(t *testing.T) {
	long := strings.Repeat("A", 129)
	for _, tc := range []struct {
		name   string
		mutate func(*model.Config)
		valid  bool
	}{
		{"unset", func(*model.Config) {}, true},
		{"cloudflare service token", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{
				"CF-Access-Client-Id":     "CF_ACCESS_ID",
				"CF-Access-Client-Secret": "CF_ACCESS_SECRET",
			}
		}, true},
		{"Host", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"Host": "VIRTUAL_HOST"}
		}, true},
		{"bearer spelling", func(c *model.Config) { c.Checks[0].HTTP.BearerEnv = "TOKEN_A" }, true},
		{"empty env map", func(c *model.Config) { c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{} }, true},
		{"empty variable name", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": ""}
		}, false},
		{"bad header name", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"Bad Header": "ENV"}
		}, false},
		{"authorization", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"Authorization": "ENV"}
		}, false},
		{"lowercase cookie", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"cookie": "ENV"}
		}, false},
		{"same map twice by case", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "ENV_A", "x-key": "ENV_B"}
		}, false},
		{"literal and secret header by case", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaders = map[string]string{"x-private": "literal"}
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Private": "ENV"}
		}, false},
		{"variable with equals", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "TOKEN=BAD"}
		}, false},
		{"variable with comma", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "TOKEN,BAD"}
		}, false},
		{"variable with space", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "TOKEN NAME"}
		}, false},
		{"variable with tab", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "TOKEN\tNAME"}
		}, false},
		{"variable with control rune", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "TOKEN\x1bNAME"}
		}, false},
		{"variable with bidi rune", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "TOKEN\u202eNAME"}
		}, false},
		{"variable too long", func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": long}
		}, false},
		{"bearer with equals", func(c *model.Config) { c.Checks[0].HTTP.BearerEnv = "TOKEN=BAD" }, false},
		{"bearer with comma", func(c *model.Config) { c.Checks[0].HTTP.BearerEnv = "TOKEN,BAD" }, false},
		{"bearer with space", func(c *model.Config) { c.Checks[0].HTTP.BearerEnv = "TOKEN NAME" }, false},
		{"bearer with bidi rune", func(c *model.Config) { c.Checks[0].HTTP.BearerEnv = "TOKEN\u202eNAME" }, false},
		{"bearer too long", func(c *model.Config) { c.Checks[0].HTTP.BearerEnv = long }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			tc.mutate(&c)
			if err := config.Validate(c); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
}

// The rule is wider than a POSIX shell identifier so every bearer_env spelling
// in use keeps working; it still refuses what a lookup cannot match and what a
// terminal or a reader could not see.
func TestSecretEnvNameRule(t *testing.T) {
	for _, name := range []string{"TOKEN", "CF_ACCESS_ID", "cf.access-id", "1", "a-b", strings.Repeat("A", 128)} {
		if !config.SecretEnvName(name) {
			t.Fatalf("refused %q", name)
		}
	}
	for _, name := range []string{"", "TOKEN=BAD", "TOKEN,BAD", "TOKEN NAME", "TOKEN\tNAME", "TOKEN\nNAME", "TOKEN\x00NAME", "TOKEN\x7fNAME", "TOKEN\u202eNAME", strings.Repeat("A", 129)} {
		if config.SecretEnvName(name) {
			t.Fatalf("accepted %q", name)
		}
	}
}

// Errors refuse a configuration without repeating the rejected header or
// variable name into logs and terminal output.
func TestRequestHeaderEnvErrorsCarryNoNames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		header   string
		variable string
	}{
		{"bad header", "Distinctive Header", "DISTINCTIVE_VARIABLE"},
		{"bad variable", "X-Distinctive-Key", "DISTINCTIVE\rVARIABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{tc.header: tc.variable}
			err := config.Validate(c)
			if err == nil {
				t.Fatal("invalid secret request header accepted")
			}
			for _, part := range []string{tc.header, tc.variable} {
				if strings.Contains(err.Error(), part) {
					t.Fatalf("error carries %q: %v", part, err)
				}
			}
		})
	}
}

// request_header_envs is an http request option: another kind that accepts
// HTTP options (mihomo) must refuse it rather than count a credential its
// adapter would never send.
func TestRequestHeaderEnvsAreHTTPOnly(t *testing.T) {
	c := testkit.Config()
	c.Checks[0].Kind = "mihomo"
	// Plain http would fail HTTPCredentials first if the kind check did not
	// come before it.
	c.Checks[0].Target = "http://203.0.113.1:9090/"
	c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"X-Key": "PRIVATE_ENV"}
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "request_header_envs is supported only by http checks") {
		t.Fatalf("mihomo accepted request_header_envs: %v", err)
	}
}

// A nonempty map sends a credential, so a plain http target is refused except
// direct literal loopback without a proxy or a remote resolve override.
func TestRequestHeaderEnvsRequireProtectedTransport(t *testing.T) {
	for _, tc := range []struct {
		target, resolve string
		proxy, allowed  bool
	}{
		{"http://example.invalid/", "", false, false},
		{"http://example.invalid/", "127.0.0.1", false, false},
		{"http://127.0.0.1/", "192.0.2.1", false, false},
		{"http://127.0.0.1/", "", true, false},
		{"http://127.0.0.1/", "", false, true},
		{"http://[::1]/", "", false, true},
		{"https://example.invalid/", "", true, true},
	} {
		for _, name := range []string{"X-API-Key", "Host"} {
			x := model.Check{Kind: "http", Target: tc.target, ResolveIP: tc.resolve, HTTP: model.HTTPOptions{RequestHeaderEnvs: map[string]string{name: "PRIVATE_ENV"}}}
			c := model.Config{}
			if tc.proxy {
				x.Path = "p"
				c.Paths = []model.Path{{ID: "p", Proxies: []model.Proxy{{Kind: "http-connect", Address: "127.0.0.1:8080"}}}}
			}
			if e := config.HTTPCredentials(c, x); (e == nil) != tc.allowed {
				t.Fatalf("%+v %s: %v", tc, name, e)
			}
		}
	}
	// The same rule applies when the whole topology is validated.
	for _, tc := range []struct {
		target  string
		allowed bool
	}{{"http://203.0.113.1/", false}, {"http://127.0.0.1/", true}, {"https://203.0.113.1/", true}} {
		c := testkit.Config()
		c.Checks[0].Target, c.Checks[0].Endpoint = tc.target, ""
		c.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{"CF-Access-Client-Secret": "CF_ACCESS_SECRET"}
		if err := config.Validate(c); (err == nil) != tc.allowed {
			t.Fatalf("%s: %v", tc.target, err)
		}
	}
}

// The map is inside HTTPOptions, so its variable names are part of the
// measurement contract while the values stay only in the worker's
// environment. Unset it writes nothing and leaves every contract unchanged;
// setting or changing a name changes that check's contract. Public clears it
// as it clears the literal request headers.
func TestRequestHeaderEnvsContractRedactionAndClone(t *testing.T) {
	c := testkit.Config()
	before := config.Contracts(c)
	config.Defaults(&c)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"request_header_envs"`) {
		t.Fatal("default config carries request_header_envs")
	}
	if !maps.Equal(config.Contracts(c), before) {
		t.Fatal("unset request_header_envs changed a contract")
	}

	withHeaders := testkit.Config()
	contract := config.Contracts(withHeaders)[withHeaders.Checks[0].ID]
	withHeaders.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{
		"CF-Access-Client-Id":     "CF_ACCESS_ID",
		"CF-Access-Client-Secret": "CF_ACCESS_SECRET",
	}
	if config.Contracts(withHeaders)[withHeaders.Checks[0].ID] == contract {
		t.Fatal("request_header_envs did not change the check's contract")
	}
	names := config.Contracts(withHeaders)[withHeaders.Checks[0].ID]
	withHeaders.Checks[0].HTTP.RequestHeaderEnvs["CF-Access-Client-Secret"] = "ANOTHER_SECRET"
	if config.Contracts(withHeaders)[withHeaders.Checks[0].ID] == names {
		t.Fatal("a changed environment variable name did not change the contract")
	}
	withHeaders.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{
		"CF-Access-Client-Id": "CF_ACCESS_ID",
		"X-Other":             "CF_ACCESS_SECRET",
	}
	if config.Contracts(withHeaders)[withHeaders.Checks[0].ID] == names {
		t.Fatal("a changed header name did not change the contract")
	}
	// An empty map is the same execution as no map; omitempty writes neither.
	empty := testkit.Config()
	empty.Checks[0].HTTP.RequestHeaderEnvs = map[string]string{}
	if config.Contracts(empty)[empty.Checks[0].ID] != contract {
		t.Fatal("an empty map changed the contract")
	}

	if got := config.Public(withHeaders).Checks[0].HTTP.RequestHeaderEnvs; got != nil {
		t.Fatalf("public topology keeps secret header environment names: %#v", got)
	}
	clone := config.Clone(withHeaders)
	clone.Checks[0].HTTP.RequestHeaderEnvs["CF-Access-Client-Id"] = "OTHER_ID"
	if withHeaders.Checks[0].HTTP.RequestHeaderEnvs["CF-Access-Client-Id"] != "CF_ACCESS_ID" {
		t.Fatal("clone shares the secret header environment map")
	}
}
