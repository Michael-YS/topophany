package config

import (
	"encoding/json"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func revisionFixture(t testing.TB) model.Config {
	t.Helper()
	var c model.Config
	if err := json.Unmarshal([]byte(`{"project":"revision-test","endpoints":[{}],"checks":[{}],"paths":[{"proxies":[{}]}]}`), &c); err != nil {
		t.Fatal(err)
	}
	c.Endpoints[0].Address = "https://example.test/check?secret=one"
	c.Checks[0].Target = "https://example.test/check?secret=one"
	c.Checks[0].HTTP.BearerEnv = "TOKEN_A"
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"Authorization": "private-a"}
	c.Checks[0].Adapter.TokenEnv = "ADAPTER_A"
	c.Checks[0].CAFile = "/private/ca-a.pem"
	p := &c.Paths[0].Proxies[0]
	p.Username, p.PasswordEnv = "alice", "PASSWORD_A"
	p.SSHIdentityFile, p.SSHKnownHosts, p.CAFile = "/private/key-a", "/private/hosts-a", "/private/proxy-ca-a"
	return c
}

// Every form an address can take loses its user information, query and
// fragment before viewers see it.
func TestPublicURLKeepsNoUserQueryOrFragment(t *testing.T) {
	for in, want := range map[string]string{
		"https://u:p@example.test:8443/a/b?k=v#frag": "https://example.test:8443/a/b?redacted",
		"https://example.test/a#frag":                "https://example.test/a",
		"https://example.test/a%23b#frag%20x":        "https://example.test/a%23b",
		"https://[2001:db8::1]:443/?q#f":             "https://[2001:db8::1]:443/?redacted",
		"http://[fe80::1%25eth0]:80/x#f":             "http://[fe80::1%25eth0]:80/x",
		"example.test:443#frag":                      "example.test:443",
		"192.0.2.1:443#frag":                         "192.0.2.1:443",
		"[2001:db8::1]:22#frag":                      "[2001:db8::1]:22",
		"user:pass@example.test:443?q=1#frag":        "example.test:443?redacted",
		"user:pass@192.0.2.1:22":                     "192.0.2.1:22",
		"example.test":                               "example.test",
		"https://example.test/no-secret":             "https://example.test/no-secret",
		"https://example.test/%zz#frag":              "https://example.test/%zz",
		"https://example.test/?#":                    "https://example.test/?",
	} {
		if got := PublicURL(in); got != want {
			t.Errorf("PublicURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPublicRevisionIgnoresEveryRedactedField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*model.Config)
	}{
		{"endpoint_query", func(c *model.Config) { c.Endpoints[0].Address = "https://example.test/check?secret=two" }},
		{"target_query", func(c *model.Config) { c.Checks[0].Target = "https://example.test/check?secret=two" }},
		{"url_userinfo", func(c *model.Config) { c.Endpoints[0].Address = "https://user:password@example.test/check?secret=one" }},
		{"endpoint_fragment", func(c *model.Config) { c.Endpoints[0].Address = "https://example.test/check?secret=one#secret-two" }},
		{"target_fragment", func(c *model.Config) { c.Checks[0].Target = "https://example.test/check?secret=one#secret-two" }},
		{"bearer_env", func(c *model.Config) { c.Checks[0].HTTP.BearerEnv = "TOKEN_B" }},
		{"adapter_env", func(c *model.Config) { c.Checks[0].Adapter.TokenEnv = "ADAPTER_B" }},
		{"header_value", func(c *model.Config) { c.Checks[0].HTTP.RequestHeaders["Authorization"] = "private-b" }},
		{"header_name", func(c *model.Config) { c.Checks[0].HTTP.RequestHeaders["X-Private"] = "private-b" }},
		{"check_ca", func(c *model.Config) { c.Checks[0].CAFile = "/private/ca-b.pem" }},
		{"proxy_user", func(c *model.Config) { c.Paths[0].Proxies[0].Username = "bob" }},
		{"proxy_password", func(c *model.Config) { c.Paths[0].Proxies[0].PasswordEnv = "PASSWORD_B" }},
		{"ssh_identity", func(c *model.Config) { c.Paths[0].Proxies[0].SSHIdentityFile = "/private/key-b" }},
		{"known_hosts", func(c *model.Config) { c.Paths[0].Proxies[0].SSHKnownHosts = "/private/hosts-b" }},
		{"proxy_ca", func(c *model.Config) { c.Paths[0].Proxies[0].CAFile = "/private/proxy-ca-b" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := revisionFixture(t)
			private, public := Revision(c), PublicRevision(c)
			tc.change(&c)
			before, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if Revision(c) == private {
				t.Fatal("private execution identity ignored a private change")
			}
			if PublicRevision(c) != public {
				t.Fatal("public digest provides an offline private-field oracle")
			}
			if PublicRevision(Public(c)) != public {
				t.Fatal("public revision is not reproducible from public topology")
			}
			after, _ := json.Marshal(c)
			if string(before) != string(after) {
				t.Fatal("revision calculation mutated executor configuration")
			}
		})
	}
}

func TestPublicRevisionTracksVisibleChangesAndCanonicalHeaders(t *testing.T) {
	c := revisionFixture(t)
	before := PublicRevision(c)
	c.Project = "another-project"
	if PublicRevision(c) == before {
		t.Fatal("visible topology change did not change public revision")
	}
	c.Checks[0].HTTP.RequestHeaders = nil
	nilRevision := PublicRevision(c)
	c.Checks[0].HTTP.RequestHeaders = map[string]string{}
	if PublicRevision(c) != nilRevision {
		t.Fatal("nil/empty private headers distinguishable to viewers")
	}
}

func FuzzPublicRevisionPrivateHeaders(f *testing.F) {
	f.Add("Authorization", "first", "second")
	f.Add("X-Private", "", "\x00\x1b\n")
	f.Fuzz(func(t *testing.T, key, first, second string) {
		c := revisionFixture(t)
		public := PublicRevision(c)
		c.Checks[0].HTTP.RequestHeaders = map[string]string{key: first}
		if PublicRevision(c) != public {
			t.Fatal("private header changed public revision")
		}
		c.Checks[0].HTTP.RequestHeaders[key] = second
		if PublicRevision(c) != public {
			t.Fatal("replacement header changed public revision")
		}
	})
}
