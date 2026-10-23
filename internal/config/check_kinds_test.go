package config

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestKindContractsPreserveWireIdentity(t *testing.T) {
	expected := map[string]string{"http": "b3acb924aa9d8194ee56cf5dfed58457ec884f3f7ea9c6d0f5fbe0e9e8a8f952", "tcp": "c66ac2cd5cc8a427282ceaa127162fb759dab63ec93ac19a705f9cc80222faf8", "tls": "a021e48ba5e2fb529cab5841a2591eb89eb451a495855392c0ff5ae9ae36470e", "ssh-banner": "ddaa482ef4fa6816e4755dd6a05f5f6db7b4d1b2da7c3c328253a2bbba4d77d7", "dns": "3c90ace2b7c430ab5fe92fba5eb9e5c5931458eb0c1b6dbb4de0b72a5900d768", "udp-echo": "13614b78ba437d329f102d752a5e273a2f3a57fa6d811885b882a0b38a9b95bb", "icmp": "6d3b88f9f77d33ddd8bbaad6fdb833c824ad1cc0e37f930f6c7dda8cdb1f5d98", "mihomo": "a4a85615e6049a764c1361fdf65439af476ec12155b1ba617212097b4537902a", "tailscale": "11aed39c4c729cb2b2750251de70099474f408a4307f9e69f971e5607125fc47", "wireguard": "596fe50e9174a056f08551fa6dfc6ed0c79f399a2660886fce478c0d62753075", "host": "8a0dcb642a153ed3436307208e455b715a07eb6889ddf71f73217ad43fba7ffa", "cloudflared": "c117caa9228e1ba69e44206a6a7ed698e3e404addeb7273add4f86fe23d900dc", "cloudflare": "ae5f47307f671bf58225b047ed1d116238019258cef50b36c6151e282eef9c12", "external": "8892505c638897354eaa68589a2dda80acd36f699054a7ef67c46ca11e5eb683"}
	for _, kind := range []string{"http", "tcp", "tls", "ssh-banner", "dns", "udp-echo", "icmp", "mihomo", "tailscale", "wireguard", "host", "cloudflared", "cloudflare", "external"} {
		x := model.Check{ID: "check", Observer: "local", Kind: kind, Target: "https://example.invalid/", TimeoutSeconds: 8, HTTP: model.HTTPOptions{Method: "GET"}, DNS: model.DNSOptions{Name: "example.invalid"}, Adapter: model.AdapterOptions{TokenEnv: "TOPO_GOLDEN_TOKEN"}, Host: &model.HostOptions{}}
		if got := Contract(model.Config{Project: "golden"}, x); got != expected[kind] {
			t.Errorf("%s contract changed: %s", kind, got)
		}
	}
}

func TestEditableKindsAreKnownAndDetached(t *testing.T) {
	names := EditableCheckKinds()
	seen := map[string]bool{}
	for _, name := range names {
		kind, ok := checkKindOf(name)
		if !ok || seen[name] || name == "external" {
			t.Fatalf("invalid editor kind %q", name)
		}
		if kind.validate == nil && name != "host" {
			t.Fatalf("kind %s has no validator", name)
		}
		seen[name] = true
	}
	names[0] = "mutated"
	if EditableCheckKinds()[0] == "mutated" {
		t.Fatal("caller mutated catalog")
	}
	if _, ok := checkKindOf("unknown"); ok {
		t.Fatal("unknown kind accepted")
	}
}
