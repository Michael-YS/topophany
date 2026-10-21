package config_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func externalConfig(t testing.TB) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Sources = []model.Source{{
		ID: "probe-api", Name: "Probe API", Adapter: "synthetic-list", Observer: "cloud",
		Target: "https://probe.invalid/v1/objects?token=secret", CredentialEnv: "TOPH_PROBE_TOKEN",
		IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90,
		MaxResponseBytes: 2 << 20, MaxPages: 3, MaxObjects: 100, MaxReportBytes: 16 << 10,
	}}
	c.Checks = append(c.Checks,
		model.Check{ID: "vm-resources", Name: "VM resources", Observer: "cloud", Service: "web", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 120, PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: floatPointer(90), MemoryPercentMax: floatPointer(85), DiskPercentMax: floatPointer(80)}}},
		model.Check{ID: "web-status", Name: "Web status", Observer: "cloud", Kind: "external", Service: "web", External: &model.ExternalBinding{Source: "probe-api", Object: "service/web", Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 60}},
	)
	config.Defaults(&c)
	return c
}

func floatPointer(v float64) *float64 { return &v }

func TestExternalSourcesDefaultsValidationAndStrictDecode(t *testing.T) {
	c := externalConfig(t)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	for _, x := range c.Checks {
		if x.Kind != "external" {
			continue
		}
		if x.IntervalSeconds != 30 || x.TimeoutSeconds != 5 || x.StaleSeconds != 90 {
			t.Fatalf("%s did not inherit source scheduling: %+v", x.ID, x)
		}
		if x.HTTP.Method != "" || x.HTTP.MaxBodyBytes != 0 || x.DNS.Type != "" {
			t.Fatalf("%s inherited transport defaults it cannot use: %+v", x.ID, x)
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	round, err := config.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if config.Revision(round) != config.Revision(c) {
		t.Fatal("external configuration did not roundtrip")
	}
	bad := strings.Replace(string(raw), `"adapter":"synthetic-list"`, `"adapter":"synthetic-list","unknown":true`, 1)
	if _, err := config.Decode(strings.NewReader(bad)); err == nil {
		t.Fatal("unknown source field was ignored")
	}
	if err := config.ValidatePublic(config.Public(c)); err != nil {
		t.Fatalf("public external topology does not validate: %v", err)
	}
}

func TestExternalContractIdentity(t *testing.T) {
	c := externalConfig(t)
	x := c.Checks[len(c.Checks)-2]
	base := config.Contract(c, x)
	for name, change := range map[string]func(*model.Config){
		"source target":  func(c *model.Config) { c.Sources[0].Target = "https://other.invalid/v1/objects" },
		"source adapter": func(c *model.Config) { c.Sources[0].Adapter = "synthetic-other" },
		"source budget":  func(c *model.Config) { c.Sources[0].MaxResponseBytes = 3 << 20 },
		"object":         func(c *model.Config) { c.Checks[len(c.Checks)-2].External.Object = "vm/other" },
		"node": func(c *model.Config) {
			c.Checks[len(c.Checks)-2].External.Node = "home"
			c.Checks[len(c.Checks)-2].Service = ""
		},
		"max age":      func(c *model.Config) { c.Checks[len(c.Checks)-2].External.MaxAgeSeconds = 300 },
		"primary disk": func(c *model.Config) { c.Checks[len(c.Checks)-2].External.PrimaryDisk = "data" },
		"assertion": func(c *model.Config) {
			c.Checks[len(c.Checks)-2].External.Assertions.CPUPercentMax = floatPointer(50)
		},
		"service": func(c *model.Config) { c.Checks[len(c.Checks)-2].Service = "" },
	} {
		t.Run(name, func(t *testing.T) {
			next := config.Clone(c)
			change(&next)
			if err := config.Validate(next); err != nil {
				t.Fatalf("changed config invalid: %v", err)
			}
			if got := config.Contract(next, next.Checks[len(next.Checks)-2]); got == base {
				t.Fatal("binding measurement change did not reset its contract")
			}
		})
	}
	for name, change := range map[string]func(*model.Config){
		"check name":      func(c *model.Config) { c.Checks[len(c.Checks)-2].Name = "Renamed" },
		"check notes":     func(c *model.Config) { c.Checks[len(c.Checks)-2].Notes = "reviewed" },
		"source name":     func(c *model.Config) { c.Sources[0].Name = "Renamed source" },
		"source notes":    func(c *model.Config) { c.Sources[0].Notes = "reviewed" },
		"credential name": func(c *model.Config) { c.Sources[0].CredentialEnv = "OTHER_PROBE_TOKEN" },
		"source schedule": func(c *model.Config) {
			c.Sources[0].IntervalSeconds = 60
			c.Checks[len(c.Checks)-2].IntervalSeconds = 60
			c.Checks[len(c.Checks)-1].IntervalSeconds = 60
		},
		"confirmation": func(c *model.Config) { c.Checks[len(c.Checks)-2].FailureThreshold = 5 },
	} {
		t.Run(name, func(t *testing.T) {
			next := config.Clone(c)
			change(&next)
			if err := config.Validate(next); err != nil {
				t.Fatalf("changed config invalid: %v", err)
			}
			if got := config.Contract(next, next.Checks[len(next.Checks)-2]); got != base {
				t.Fatal("presentation or credential reference changed measurement identity")
			}
		})
	}
}

func TestNativeContractsDoNotChangeWhenSourcesAreAdded(t *testing.T) {
	base := testkit.Config()
	before := config.Contracts(base)
	with := externalConfig(t)
	after := config.Contracts(with)
	for id, contract := range before {
		if after[id] != contract {
			t.Fatalf("native check %s contract changed", id)
		}
	}
}

func TestExternalPublicTopologyRemovesCredentialsAndQueries(t *testing.T) {
	c := externalConfig(t)
	public := config.Public(c)
	if public.Sources[0].CredentialEnv != "" || public.Sources[0].CAFile != "" {
		t.Fatal("public source retained a credential reference")
	}
	raw, _ := json.Marshal(public)
	for _, secret := range []string{"TOPH_PROBE_TOKEN", "token=secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("public topology leaked %q", secret)
		}
	}
	c.Sources[0].Target = "https://changed.invalid/"
	if public.Sources[0].Target != "https://probe.invalid/v1/objects?redacted" {
		t.Fatal("public source shares storage with executor configuration")
	}
}

func TestExternalConfigRefusals(t *testing.T) {
	for name, change := range map[string]func(*model.Config){
		"missing binding": func(c *model.Config) {
			c.Checks = append(c.Checks, model.Check{ID: "bad-external", Observer: "cloud", Kind: "external"})
			config.Defaults(c)
		},
		"binding on native kind": func(c *model.Config) {
			c.Checks[0].External = &model.ExternalBinding{Source: "probe-api", Object: "x", Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 60}
		},
		"missing source": func(c *model.Config) { c.Checks[len(c.Checks)-2].External.Source = "missing" },
		"observer mismatch": func(c *model.Config) {
			c.Checks[len(c.Checks)-2].Observer = "home"
			c.Checks[len(c.Checks)-2].IntervalSeconds = 30
		},
		"schedule conflict": func(c *model.Config) { c.Checks[len(c.Checks)-2].IntervalSeconds = 60 },
		"timeout conflict":  func(c *model.Config) { c.Checks[len(c.Checks)-2].TimeoutSeconds = 6 },
		"transport override": func(c *model.Config) {
			c.Checks[len(c.Checks)-2].Target = "https://override.invalid/"
		},
		"HTTP options": func(c *model.Config) { c.Checks[len(c.Checks)-2].HTTP.Contains = "x" },
		"unknown report": func(c *model.Config) {
			c.Checks[len(c.Checks)-2].External.Report = "metric"
		},
		"object identity": func(c *model.Config) { c.Checks[len(c.Checks)-2].External.Object = "bad object\n" },
		"missing node":    func(c *model.Config) { c.Checks[len(c.Checks)-2].External.Node = "missing" },
		"foreign service": func(c *model.Config) { c.Checks[len(c.Checks)-2].Service = "dsh" },
		"zero max age":    func(c *model.Config) { c.Checks[len(c.Checks)-2].External.MaxAgeSeconds = 0 },
		"short timeout":   func(c *model.Config) { c.Sources[0].TimeoutSeconds = 0; c.Checks[len(c.Checks)-2].TimeoutSeconds = 0 },
		"status disk": func(c *model.Config) {
			c.Checks[len(c.Checks)-1].External.PrimaryDisk = "root"
		},
		"disk assertion without primary": func(c *model.Config) {
			c.Checks[len(c.Checks)-2].External.PrimaryDisk = ""
		},
		"NaN assertion": func(c *model.Config) {
			c.Checks[len(c.Checks)-2].External.Assertions.CPUPercentMax = func() *float64 { v := math.NaN(); return &v }()
		},
		"ambiguous resources": func(c *model.Config) {
			other := c.Checks[len(c.Checks)-2]
			other.ID = "vm-resources-2"
			c.Checks = append(c.Checks, other)
		},
		"explicit resources unknown check": func(c *model.Config) {
			for i := range c.Nodes {
				if c.Nodes[i].ID == "dmit" {
					c.Nodes[i].ResourceChecks = []string{"missing"}
				}
			}
		},
		"source missing observer": func(c *model.Config) { c.Sources[0].Observer = "missing" },
		"source adapter shape":    func(c *model.Config) { c.Sources[0].Adapter = "Synthetic: List" },
		"source budget":           func(c *model.Config) { c.Sources[0].MaxResponseBytes = 1 << 30 },
		"source pages":            func(c *model.Config) { c.Sources[0].MaxPages = model.MaxSourcePages + 1 },
		"source objects":          func(c *model.Config) { c.Sources[0].MaxObjects = model.MaxSourceObjects + 1 },
		"source report budget":    func(c *model.Config) { c.Sources[0].MaxReportBytes = model.MaxReportBytes + 1 },
		"source stale":            func(c *model.Config) { c.Sources[0].StaleSeconds = 1 },
		"source path scope":       func(c *model.Config) { c.Sources[0].Path = "direct" },
		"source plaintext secret": func(c *model.Config) { c.Sources[0].Target = "http://probe.invalid/v1/objects" },
		"source credential name":  func(c *model.Config) { c.Sources[0].CredentialEnv = "bad name" },
		"source target control":   func(c *model.Config) { c.Sources[0].Target = "https://probe.invalid/\x1b" },
		"source duplicate":        func(c *model.Config) { c.Sources = append(c.Sources, c.Sources[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			c := externalConfig(t)
			change(&c)
			if err := config.Validate(c); err == nil {
				t.Fatal("invalid external configuration accepted")
			}
		})
	}
}

func TestExternalResourceSelectionListAllowsOrderedFallback(t *testing.T) {
	c := externalConfig(t)
	other := c.Checks[len(c.Checks)-2]
	other.ID = "vm-resources-2"
	other.External = &model.ExternalBinding{Source: "probe-api", Object: "vm/dmit-2", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 120, PrimaryDisk: "root"}
	c.Checks = append(c.Checks, other)
	for i := range c.Nodes {
		if c.Nodes[i].ID == "dmit" {
			c.Nodes[i].ResourceChecks = []string{"vm-resources-2", "vm-resources"}
		}
	}
	if err := config.Validate(c); err != nil {
		t.Fatalf("ordered resource fallback refused: %v", err)
	}
	// A duplicate entry cannot silently weight one source twice.
	c.Nodes[2].ResourceChecks = []string{"vm-resources-2", "vm-resources-2"}
	if err := config.Validate(c); err == nil {
		t.Fatal("duplicate ordered resource check accepted")
	}
}

func TestExternalConfigCloneDetachesBindingsAndResourceLists(t *testing.T) {
	c := externalConfig(t)
	copy := config.Clone(c)
	if copy.Sources[0].ID != c.Sources[0].ID || copy.Checks[len(copy.Checks)-2].External == c.Checks[len(c.Checks)-2].External {
		t.Fatal("clone did not copy external state")
	}
	copy.Sources[0].Target = "https://clone.invalid/"
	copy.Checks[len(copy.Checks)-2].External.Assertions.CPUPercentMax = floatPointer(10)
	if c.Sources[0].Target == copy.Sources[0].Target || *c.Checks[len(c.Checks)-2].External.Assertions.CPUPercentMax == 10 {
		t.Fatal("clone shares external writable state")
	}
}

func TestExternalContractIncludesResolvedRoute(t *testing.T) {
	c := externalConfig(t)
	c.Paths = append(c.Paths, model.Path{ID: "source-route", Observer: "cloud", Proxies: []model.Proxy{
		{Kind: "socks5", Address: "127.0.0.1:1080"},
		{Kind: "http-connect", Address: "127.0.0.1:8080", ServerName: "proxy.example"},
	}})
	c.Sources[0].Path = "source-route"
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	x := c.Checks[len(c.Checks)-2]
	base := config.Contract(c, x)
	for name, change := range map[string]func(*model.Config){
		"proxy address": func(c *model.Config) { c.Paths[len(c.Paths)-1].Proxies[0].Address = "127.0.0.1:1081" },
		"proxy kind":    func(c *model.Config) { c.Paths[len(c.Paths)-1].Proxies[0].Kind = "http-connect" },
		"proxy order": func(c *model.Config) {
			p := c.Paths[len(c.Paths)-1].Proxies
			p[0], p[1] = p[1], p[0]
		},
		"proxy tls name": func(c *model.Config) { c.Paths[len(c.Paths)-1].Proxies[1].ServerName = "other.example" },
		"proxy ca":       func(c *model.Config) { c.Paths[len(c.Paths)-1].Proxies[1].CAFile = "/private/proxy-ca.pem" },
		"proxy user":     func(c *model.Config) { c.Paths[len(c.Paths)-1].Proxies[0].Username = "route-user" },
		"extra hop": func(c *model.Config) {
			c.Paths[len(c.Paths)-1].Proxies = append(c.Paths[len(c.Paths)-1].Proxies, model.Proxy{Kind: "socks5", Address: "127.0.0.1:1082"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			next := config.Clone(c)
			change(&next)
			if err := config.Validate(next); err != nil {
				t.Fatalf("changed route invalid: %v", err)
			}
			if got := config.Contract(next, next.Checks[len(next.Checks)-2]); got == base {
				t.Fatal("route measurement change did not reset the external contract")
			}
		})
	}
	// A path rename with identical hops, or presentation-only metadata, is
	// not a measurement change.
	for name, change := range map[string]func(*model.Config){
		"path id": func(c *model.Config) {
			c.Paths = append(c.Paths, model.Path{ID: "renamed-route", Observer: "cloud", Proxies: append([]model.Proxy(nil), c.Paths[len(c.Paths)-1].Proxies...)})
			c.Sources[0].Path = "renamed-route"
		},
		"source name": func(c *model.Config) { c.Sources[0].Name = "Renamed source" },
		"source note": func(c *model.Config) { c.Sources[0].Notes = "reviewed" },
		"check name":  func(c *model.Config) { c.Checks[len(c.Checks)-2].Name = "Renamed check" },
		"check note":  func(c *model.Config) { c.Checks[len(c.Checks)-2].Notes = "reviewed" },
	} {
		t.Run(name, func(t *testing.T) {
			next := config.Clone(c)
			change(&next)
			if err := config.Validate(next); err != nil {
				t.Fatalf("changed metadata invalid: %v", err)
			}
			if got := config.Contract(next, next.Checks[len(next.Checks)-2]); got != base {
				t.Fatal("presentation change reset the external contract")
			}
		})
	}
}

func TestExternalPublicContractUsesRedactedRouteOnly(t *testing.T) {
	c := externalConfig(t)
	c.Paths = append(c.Paths, model.Path{ID: "source-route", Observer: "cloud", Proxies: []model.Proxy{
		{Kind: "socks5", Address: "127.0.0.1:1080", Username: "private-user", PasswordEnv: "PRIVATE_PASSWORD"},
	}})
	c.Sources[0].Path = "source-route"
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	x := c.Checks[len(c.Checks)-2]
	private := config.Contract(c, x)
	publicContracts := config.PublicContracts(c)
	publicTopology := config.Public(c)
	publicCheck, ok := config.Check(publicTopology, x.ID)
	if !ok {
		t.Fatal("public topology lost the external check")
	}
	if got := publicContracts[x.ID]; got != config.Contract(publicTopology, publicCheck) {
		t.Fatal("public contract was not recomputed from the redacted topology")
	}
	if publicContracts[x.ID] == private {
		t.Fatal("public contract reused the private measurement hash")
	}
	raw, err := json.Marshal(publicTopology)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-user", "PRIVATE_PASSWORD", "TOPH_PROBE_TOKEN", "token=secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("public topology leaked %q", secret)
		}
	}
	// Private credential edits cannot be observed through the public digest.
	changed := config.Clone(c)
	changed.Paths[len(changed.Paths)-1].Proxies[0].Username = "other-private-user"
	changed.Paths[len(changed.Paths)-1].Proxies[0].PasswordEnv = "OTHER_PRIVATE_PASSWORD"
	if err := config.Validate(changed); err != nil {
		t.Fatal(err)
	}
	if got := config.PublicContracts(changed)[x.ID]; got != publicContracts[x.ID] {
		t.Fatal("public contract changed when only private credentials changed")
	}
	if got := config.Contract(changed, changed.Checks[len(changed.Checks)-2]); got == private {
		t.Fatal("private route credential change did not start a new measurement contract")
	}
}
