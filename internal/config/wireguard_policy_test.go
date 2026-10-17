package config_test

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
)

func TestWireGuardPolicyContractAndClone(t *testing.T) {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind = "wireguard"
	x.Target = ""
	x.Endpoint = ""
	x.Path = ""
	x.Adapter = model.AdapterOptions{Interface: "wg0", Peer: "peer"}
	baseline := config.Contract(c, *x)
	zero := 0
	x.Adapter.ExpectedKeepaliveSeconds = &zero
	x.Adapter.ExpectedAllowedIPs = []string{"10.0.0.0/24"}
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	if config.Contract(c, *x) == baseline {
		t.Fatal("policy absent from measurement contract")
	}
	copy := config.Clone(c)
	*copy.Checks[0].Adapter.ExpectedKeepaliveSeconds = 25
	copy.Checks[0].Adapter.ExpectedAllowedIPs[0] = "192.0.2.0/24"
	if *x.Adapter.ExpectedKeepaliveSeconds != 0 || x.Adapter.ExpectedAllowedIPs[0] != "10.0.0.0/24" {
		t.Fatal("policy clone aliases original")
	}
}
