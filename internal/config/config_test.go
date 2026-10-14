package config_test

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"strings"
	"testing"
)

func TestConfigIdentityAndStrictDecode(t *testing.T) {
	c := testkit.Config()
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(c)
	got, e := config.Decode(strings.NewReader(string(b)))
	if e != nil {
		t.Fatal(e)
	}
	if config.Revision(got) != config.Revision(c) {
		t.Fatal("roundtrip changed revision")
	}
	if c.Endpoints[2].Node == c.Endpoints[3].Node {
		t.Fatal("fixture identities should differ despite shared address space")
	}
	for _, bad := range []string{string(b) + ` {}`, strings.Replace(string(b), `"schema":1`, `"schema":99`, 1), strings.Replace(string(b), `"schema":1`, `"unknown":1,"schema":1`, 1), strings.Repeat("x", (4<<20)+1)} {
		if _, e := config.Decode(strings.NewReader(bad)); e == nil {
			t.Fatal("accepted invalid JSON/schema")
		}
	}
}
func TestConfigRejectsBrokenTopology(t *testing.T) {
	tests := map[string]func(*model.Config){
		"duplicate node identity":        func(c *model.Config) { c.Nodes = append(c.Nodes, c.Nodes[0]) },
		"loopback without observer":      func(c *model.Config) { c.Endpoints[4].Observer = "" },
		"loopback from foreign observer": func(c *model.Config) { c.Checks[0].Endpoint = "forward-loop" },
		"unknown endpoint network":       func(c *model.Config) { c.Endpoints[0].Network = "magic" },
		"parent cycle":                   func(c *model.Config) { c.Nodes[0].Parent = "wsl" },
		"service dependency cycle": func(c *model.Config) {
			c.Services[0].DependsOn = []string{"dsh"}
			c.Services[1].DependsOn = []string{"web"}
		},
		"path cycle":                       func(c *model.Config) { c.Paths[0].DependsOn = []string{"direct"} },
		"cross-observer path":              func(c *model.Config) { c.Checks[0].Path = "contrast" },
		"policy is not a serial transport": func(c *model.Config) { c.Paths[5].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:9"}} },
		"cannot probe abstract fallback":   func(c *model.Config) { c.Checks[0].Path = "residential" },
		"noncontiguous links":              func(c *model.Config) { c.Paths[2].Links = []string{"dmit-exit-link", "home-dmit-link"} },
		"proxy reference missing node": func(c *model.Config) {
			c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:9", Node: "missing"}}
		},
		"unsupported protocol not faked": func(c *model.Config) { c.Checks[0].Kind = "reality" },
		"via an unknown node":            func(c *model.Config) { c.Endpoints[0].Via = []string{"missing"} },
		"via its own node":               func(c *model.Config) { c.Endpoints[0].Via = []string{"dmit"} },
		"via the same edge twice":        func(c *model.Config) { c.Endpoints[0].Via = []string{"external", "external"} },
		"via too many hops": func(c *model.Config) {
			c.Endpoints[0].Via = []string{"home", "wsl", "aliyun", "s2", "aaitr", "external", "edge1", "edge2", "edge3"}
			for _, id := range []string{"edge1", "edge2", "edge3"} {
				c.Nodes = append(c.Nodes, model.Node{ID: id, Name: id, Kind: "edge"})
			}
		},
		"provider with control characters": func(c *model.Config) { c.Nodes[0].Provider = "cloud\x1b[2Jflare" },
		"runtime beyond a label":           func(c *model.Config) { c.Services[0].Runtime = strings.Repeat("w", 33) },
		"UDP over TCP chain refused": func(c *model.Config) {
			c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:9"}}
			c.Checks[0].Kind = "udp-echo"
			c.Checks[0].Target = "127.0.0.1:9"
		},
		"insecure HTTP credential literal": func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaders = map[string]string{"Authorization": "Bearer test"}
		},
		"header injection": func(c *model.Config) {
			c.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Test": "x\r\nInjected: x"}
		},
		"HTTP userinfo":       func(c *model.Config) { c.Checks[0].Target = "http://user:password@127.0.0.1/" },
		"invalid regex":       func(c *model.Config) { c.Checks[0].HTTP.BodyRegex = "[" },
		"invalid CIDR":        func(c *model.Config) { c.Checks[0].HTTP.ExpectedExit = []string{"1.2.3.4/90"} },
		"invalid binding":     func(c *model.Config) { c.Checks[0].BindIP = "not-an-ip" },
		"unbounded timeout":   func(c *model.Config) { c.Checks[0].TimeoutSeconds = 9999 },
		"too-short freshness": func(c *model.Config) { c.Checks[0].StaleSeconds = 1 },
		"zero confirmation":   func(c *model.Config) { c.Checks[0].FailureThreshold = 0 },
		"DNS invalid question": func(c *model.Config) {
			c.Checks[0].Kind = "dns"
			c.Checks[0].Target = "127.0.0.1:53"
			c.Checks[0].DNS.Name = "a..b"
		},
		"SSH requires pinned host keys": func(c *model.Config) { c.Paths[0].Proxies = []model.Proxy{{Kind: "ssh", Address: "127.0.0.1:22"}} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := testkit.Config()
			mutate(&c)
			if e := config.Validate(c); e == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
}
func TestRedactionIsDetachedAndRevisionChangesWithMeasurement(t *testing.T) {
	c := testkit.Config()
	c.Checks[0].Target = "https://test.invalid/health?token=secret-query"
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Key": "secret-header"}
	c.Checks[0].HTTP.BearerEnv = "PRIVATE_KEY"
	c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:9", Username: "private-user", PasswordEnv: "PASSWORD"}}
	original, _ := json.Marshal(c)
	redacted := config.Public(c)
	r, _ := json.Marshal(redacted)
	for _, secret := range []string{"secret-query", "secret-header", "private-user", "PRIVATE_KEY", "PASSWORD"} {
		if strings.Contains(string(r), secret) {
			t.Fatalf("redaction leaked %s", secret)
		}
	}
	after, _ := json.Marshal(c)
	if string(original) != string(after) {
		t.Fatal("export mutated live config")
	}
	d := testkit.Config()
	before := config.Revision(d)
	d.Checks[0].Target = "https://different.invalid/204"
	if config.Revision(d) == before {
		t.Fatal("changed measuring target reused series revision")
	}
}
func TestDefaultTimingAndScope(t *testing.T) {
	c := testkit.Config()
	c.Checks[0] = model.Check{ID: "new", Observer: "home", Kind: "http", Target: "http://127.0.0.1/"}
	config.Defaults(&c)
	x := c.Checks[0]
	if x.IntervalSeconds != 60 || x.StaleSeconds != 180 || x.FailureThreshold != 3 || x.RecoveryThreshold != 2 {
		t.Fatalf("bad defaults %+v", x)
	}
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
}
func FuzzDecodeTopology(f *testing.F) {
	b, _ := json.Marshal(testkit.Config())
	f.Add(b)
	f.Add([]byte(`{"schema":1}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<16 {
			t.Skip()
		}
		_, _ = config.Decode(strings.NewReader(string(b)))
	})
}

// Edges, providers and runtimes are optional: a topology that declares
// them validates, and one that does not keeps its JSON, so its revision.
func TestEdgeFieldsValidateAndStayOptional(t *testing.T) {
	c := testkit.Config()
	plain, _ := json.Marshal(c)
	for _, key := range []string{`"via"`, `"provider"`, `"runtime"`} {
		if strings.Contains(string(plain), key) {
			t.Fatal("empty field serialized", key)
		}
	}
	c.Nodes = append(c.Nodes, model.Node{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare"})
	c.Endpoints[0].Via = []string{"cf", "aliyun"}
	c.Services[0].Runtime = "workers"
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	if config.Revision(c) == config.Revision(testkit.Config()) {
		t.Fatal("declaring an edge did not change the revision")
	}
}
