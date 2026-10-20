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

// expected_pops is bounded to colo shapes: at most sixteen distinct codes
// of exactly three ASCII uppercase letters, and nothing else.
func TestExpectedPopsValidation(t *testing.T) {
	colos := func(n int) []string {
		all := []string{"HKG", "NRT", "LAX", "SFO", "SEA", "ORD", "IAD", "JFK", "LHR", "FRA", "AMS", "CDG", "SIN", "SYD", "GRU", "JNB", "HND"}
		return append([]string{}, all[:n]...)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*model.Config)
		valid  bool
	}{
		{"unset", func(*model.Config) {}, true},
		{"one", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = []string{"HKG"} }, true},
		{"sixteen", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = colos(16) }, true},
		{"seventeen", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = colos(17) }, false},
		{"lowercase", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = []string{"hkg"} }, false},
		{"two letters", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = []string{"HK"} }, false},
		{"four letters", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = []string{"HKGG"} }, false},
		{"space", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = []string{"H KG"} }, false},
		{"empty code", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = []string{""} }, false},
		{"duplicate", func(c *model.Config) { c.Checks[0].HTTP.ExpectedPops = []string{"HKG", "HKG"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			tc.mutate(&c)
			if err := config.Validate(c); (err == nil) != tc.valid {
				t.Fatalf("valid=%v; got %v", tc.valid, err)
			}
		})
	}
	c := testkit.Config()
	c.Checks[0].HTTP.ExpectedPops = []string{"HKG", "HKG"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "without duplicates") {
		t.Fatalf("duplicate error is %v", err)
	}
}

// expected_pops is inside HTTPOptions, so it is part of the measurement
// contract: unset it writes nothing and leaves a default configuration's
// contracts unchanged, and set it gives that check a new contract. It is
// not a secret, so redaction keeps it and the clone does not share it.
func TestExpectedPopsContractAndDefaultJSON(t *testing.T) {
	c := testkit.Config()
	before := config.Contracts(c)
	config.Defaults(&c)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"expected_pops"`) {
		t.Fatal("default config carries expected_pops")
	}
	if !maps.Equal(config.Contracts(c), before) {
		t.Fatal("unset expected_pops changed a contract")
	}

	declared := testkit.Config()
	base := config.Contracts(declared)[declared.Checks[0].ID]
	declared.Checks[0].HTTP.ExpectedPops = []string{"HKG", "NRT"}
	if config.Contracts(declared)[declared.Checks[0].ID] == base {
		t.Fatal("expected_pops did not change the check's contract")
	}
	if config.Validate(declared) != nil {
		t.Fatal("valid expected_pops refused")
	}
	if public := config.Public(declared).Checks[0].HTTP.ExpectedPops; len(public) != 2 || public[0] != "HKG" {
		t.Fatalf("public topology lost expected_pops: %#v", public)
	}
	clone := config.Clone(declared)
	clone.Checks[0].HTTP.ExpectedPops[0] = "LAX"
	if declared.Checks[0].HTTP.ExpectedPops[0] != "HKG" {
		t.Fatal("clone shares the expected pops")
	}
}

// Every topology shipped in examples/ was written before expected_pops, so
// none may carry it: their contract hashes are unchanged by this field being
// unset, through a JSON round trip.
func TestExampleTopologiesCarryNoExpectedPops(t *testing.T) {
	for path, c := range exampleTopologies(t) {
		before := config.Contracts(c)
		out, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), `"expected_pops"`) {
			t.Fatalf("%s carries expected_pops", path)
		}
		var back model.Config
		if err := json.Unmarshal(out, &back); err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(before, config.Contracts(back)) {
			t.Fatalf("%s: unset expected_pops changed a contract", path)
		}
	}
}
