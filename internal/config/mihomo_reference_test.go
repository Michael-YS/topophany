package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestMihomoReferenceContractCloneAndRedaction(t *testing.T) {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind = "mihomo"
	x.Adapter.ProviderReference = &model.MihomoProviderReference{URL: "https://publisher.invalid/members.json", Providers: []string{"edge-local"}, MaxAgeSeconds: 3600, TokenEnv: "PUBLISHER_A", CAFile: "/private/publisher-a.pem"}
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	before := config.Contract(c, *x)
	settle := 120
	x.Adapter.ProviderReference.SettleSeconds = &settle
	if got := config.Contract(c, *x); got != before {
		t.Fatal("settle_seconds changed the measurement contract")
	}
	settle = 600
	if got := config.Contract(c, *x); got != before {
		t.Fatal("a different settle_seconds changed the measurement contract")
	}
	x.Adapter.ProviderReference.SettleSeconds = nil
	public := config.PublicRevision(c)
	copy := config.Clone(c)
	copy.Checks[0].Adapter.ProviderReference.Providers[0] = "changed"
	if x.Adapter.ProviderReference.Providers[0] != "edge-local" {
		t.Fatal("clone aliases reference names")
	}
	x.Adapter.ProviderReference.TokenEnv = "PUBLISHER_B"
	x.Adapter.ProviderReference.CAFile = "/private/publisher-b.pem"
	if config.Contract(c, *x) == before {
		t.Fatal("execution contract ignored publisher credentials")
	}
	if config.PublicRevision(c) != public {
		t.Fatal("public revision reveals private publisher settings")
	}
	raw, _ := json.Marshal(config.Public(c))
	for _, secret := range []string{"PUBLISHER_B", "/private/publisher-b.pem"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("publisher settings leaked")
		}
	}
	before = config.Contract(c, *x)
	x.Adapter.ProviderReference.MaxAgeSeconds++
	if config.Contract(c, *x) == before {
		t.Fatal("freshness policy absent from contract")
	}
}

func TestMihomoReferenceCannotUseProxiedPlaintextLoopback(t *testing.T) {
	c := testkit.Config()
	x := &c.Checks[0]
	x.Kind = "mihomo"
	x.Adapter.ProviderReference = &model.MihomoProviderReference{URL: "http://127.0.0.1:8080/members.json", Providers: []string{"edge-local"}, MaxAgeSeconds: 3600}
	c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "192.0.2.1:1080"}}
	if config.Validate(c) == nil {
		t.Fatal("a remote proxy's loopback was trusted as a local publisher")
	}
}
