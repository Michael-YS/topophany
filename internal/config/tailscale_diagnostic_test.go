package config_test

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestTailscaleDiagnosticValidationAndContract(t *testing.T) {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind, x.Target, x.Endpoint, x.Path = "tailscale", "", "", ""
	x.TimeoutSeconds = 4
	x.Adapter = model.AdapterOptions{Peer: "peer"}
	baseline := config.Contract(c, *x)
	for _, mode := range []string{"disco", "tsmp"} {
		x.Adapter.TailscaleDiagnostic = mode
		if err := config.Validate(c); err != nil {
			t.Fatal(mode, err)
		}
		if config.Contract(c, *x) == baseline {
			t.Fatal("active diagnostic absent from measurement contract")
		}
	}
	x.Adapter.TailscaleDiagnostic = "icmp"
	if config.Validate(c) == nil {
		t.Fatal("unsupported protocol accepted")
	}
	x.Adapter.TailscaleDiagnostic, x.TimeoutSeconds = "disco", 3
	if config.Validate(c) == nil {
		t.Fatal("insufficient diagnostic timeout accepted")
	}
	x.Adapter.TailscaleDiagnostic = ""
	if err := config.Validate(c); err != nil {
		t.Fatal("passive check inherits diagnostic minimum", err)
	}
}
