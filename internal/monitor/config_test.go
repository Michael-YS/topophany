package monitor

import (
	"path/filepath"
	"testing"
)

func TestConfigRejectsInvalidBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"schema", func(c *Config) { c.Schema = 2 }},
		{"name", func(c *Config) { c.Name = "../other" }},
		{"interval syntax", func(c *Config) { c.Interval = "invalid" }},
		{"short interval", func(c *Config) { c.Interval = "999ms" }},
		{"long interval", func(c *Config) { c.Interval = "1h1s" }},
		{"relative output", func(c *Config) { c.Output = "metrics.json" }},
		{"output extension", func(c *Config) { c.Output += ".txt" }},
		{"client credentials", func(c *Config) { c.Source = "client"; c.Credentials = "relative"; c.Profile = "viewer" }},
		{"client profile", func(c *Config) {
			c.Source = "client"
			c.Credentials = filepath.Join(t.TempDir(), "credentials.json")
			c.Profile = "../other"
		}},
		{"project", func(c *Config) { c.Project = "bad project" }},
		{"observer", func(c *Config) { c.Observer = "bad observer" }},
		{"collector scope", func(c *Config) { c.Observer = "home" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			tc.change(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid monitor configuration accepted")
			}
		})
	}
	for _, interval := range []string{"1s", "1h"} {
		c := testConfig()
		c.Interval = interval
		if err := c.Validate(); err != nil {
			t.Fatal("valid boundary rejected", interval, err)
		}
	}
}
