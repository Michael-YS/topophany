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

func hostConfig() model.Config {
	c := testkit.Config()
	c.Checks = []model.Check{{ID: "resources", Observer: "home", Kind: "host", Host: &model.HostOptions{DiskPath: "/", ProcessName: "topo", MinProcesses: 1}}}
	config.Defaults(&c)
	return c
}

func TestHostConfigurationAndMeasurementContract(t *testing.T) {
	c := hostConfig()
	b, _ := json.Marshal(c)
	if _, err := config.Decode(strings.NewReader(string(b))); err != nil {
		t.Fatal(err)
	}
	contract := config.Contract(c, c.Checks[0])
	clone := config.Clone(c)
	clone.Checks[0].Host.MaxCPUPercent = 80
	if c.Checks[0].Host.MaxCPUPercent != 0 || config.Contract(clone, clone.Checks[0]) == contract {
		t.Fatal("host options shared or contract unchanged")
	}
	for _, change := range []func(*model.HostOptions){
		func(h *model.HostOptions) { h.DiskPath = "/var" },
		func(h *model.HostOptions) { h.ProcessName = "other" },
		func(h *model.HostOptions) { h.MinProcesses = 2 },
		func(h *model.HostOptions) { h.MaxProcesses = 3 },
		func(h *model.HostOptions) { h.MaxMemoryPercent = 90 },
		func(h *model.HostOptions) { h.MaxDiskPercent = 90 },
	} {
		next := config.Clone(c)
		change(next.Checks[0].Host)
		if config.Contract(next, next.Checks[0]) == contract {
			t.Fatal("measurement change did not invalidate old samples")
		}
	}
	clone.Checks[0].Host = nil
	if err := config.Validate(clone); err != nil {
		t.Fatal("default host options", err)
	}
	if err := config.ValidatePublic(config.Public(c)); err != nil {
		t.Fatal("viewer configuration", err)
	}
}

func TestHostRejectsRemoteAttributionAndInvalidOptions(t *testing.T) {
	for name, change := range map[string]func(*model.Check){
		"endpoint":            func(x *model.Check) { x.Endpoint = "dmit-http" },
		"target":              func(x *model.Check) { x.Target = "remote.invalid" },
		"path":                func(x *model.Check) { x.Path = "direct" },
		"link":                func(x *model.Check) { x.Link = "home-dmit-link" },
		"bind":                func(x *model.Check) { x.BindIP = "127.0.0.1" },
		"foreign service":     func(x *model.Check) { x.Service = "web" },
		"short timeout":       func(x *model.Check) { x.TimeoutSeconds = 1 },
		"relative filesystem": func(x *model.Check) { x.Host.DiskPath = "data" },
		"unclean filesystem":  func(x *model.Check) { x.Host.DiskPath = "/var/../tmp" },
		"percentage":          func(x *model.Check) { x.Host.MaxCPUPercent = 101 },
		"negative percentage": func(x *model.Check) { x.Host.MaxMemoryPercent = -1 },
		"not a number":        func(x *model.Check) { x.Host.MaxDiskPercent = math.NaN() },
		"too long process":    func(x *model.Check) { x.Host.ProcessName = strings.Repeat("x", 16) },
		"process arguments":   func(x *model.Check) { x.Host.ProcessName = "topo\nworker" },
		"process path":        func(x *model.Check) { x.Host.ProcessName = "/bin/topo" },
		"no process name":     func(x *model.Check) { x.Host.ProcessName = "" },
		"process bounds":      func(x *model.Check) { x.Host.MinProcesses = 3; x.Host.MaxProcesses = 2 },
		"other kind":          func(x *model.Check) { x.Kind = "tailscale"; x.Adapter.Peer = "peer" },
	} {
		t.Run(name, func(t *testing.T) {
			c := hostConfig()
			change(&c.Checks[0])
			if err := config.Validate(c); err == nil {
				t.Fatal("invalid host check accepted")
			}
		})
	}
}

func TestWindowsHostPathsAreReviewedOnAnyCollectorOS(t *testing.T) {
	for _, p := range []string{"C:/", "C:/Users/Test User", "D:\\Data", "c:\\"} {
		c := hostConfig()
		c.Checks[0].Host.DiskPath = p
		if e := config.Validate(c); e != nil {
			t.Fatalf("%q: %v", p, e)
		}
	}
	for _, p := range []string{"C:relative", "C:/Data/../Windows", "C:\\Data\\..\\Windows", "C://Data", "\\\\server\\share"} {
		c := hostConfig()
		c.Checks[0].Host.DiskPath = p
		if e := config.Validate(c); e == nil {
			t.Fatalf("unsafe or unclean path accepted: %q", p)
		}
	}
}
