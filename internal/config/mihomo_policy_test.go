package config_test

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
)

func TestMihomoPolicyContractsAndClone(t *testing.T) {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind = "mihomo"
	before := config.Contract(c, *x)
	value := false
	x.Adapter.ExpectedTUN = &value
	if config.Contract(c, *x) == before {
		t.Fatal("explicit false TUN expectation absent from contract")
	}
	x.Adapter.ExpectedProviders = map[string][]string{"pool": {"a"}}
	x.Adapter.ProviderMatchModes = map[string]string{"pool": "ordered"}
	old := config.Contract(c, *x)
	x.Adapter.ProviderMatchModes["pool"] = "set"
	if config.Contract(c, *x) == old {
		t.Fatal("comparison mode absent from contract")
	}
	clone := config.Clone(c)
	*clone.Checks[0].Adapter.ExpectedTUN = true
	clone.Checks[0].Adapter.ProviderMatchModes["pool"] = "ordered"
	if *x.Adapter.ExpectedTUN || x.Adapter.ProviderMatchModes["pool"] != "set" {
		t.Fatal("policy clone shares mutable data")
	}
}
