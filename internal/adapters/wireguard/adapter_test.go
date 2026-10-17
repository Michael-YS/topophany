package wireguard_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestWireGuardOldHandshakeIsNotFailureAndKeysAreNotRead(t *testing.T) {
	x := httpCheck("")
	x.Kind = "wireguard"
	x.Adapter = model.AdapterOptions{Interface: "wg0", Peer: "PUBLIC-PEER"}
	r := probe.Runner{Command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "wg" || len(args) != 3 || args[0] != "show" || args[1] != "wg0" {
			t.Fatal(name, args)
		}
		switch args[2] {
		case "latest-handshakes":
			return []byte("PUBLIC-PEER\t100\n"), nil
		case "transfer":
			return []byte("PUBLIC-PEER\t123\t456\n"), nil
		default:
			t.Fatal("sensitive or unexpected query", args)
			return nil, nil
		}
	}}
	o := r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Connectivity || o.Evidence != "reported" || o.Facts["rx_bytes"] != "123" {
		t.Fatal(o)
	}
	if o.Facts["last_handshake"] != time.Unix(100, 0).UTC().Format(time.RFC3339) {
		t.Fatal("old handshake replaced with fresh timestamp")
	}
	b, _ := json.Marshal(o)
	if strings.Contains(string(b), "PRIVATE-KEY") || strings.Contains(string(b), "PRESHARED-SECRET") {
		t.Fatal("WireGuard private key leaked")
	}
	x.Adapter.Peer = "missing"
	if o = r.Run(context.Background(), testkit.Config(), x); o.Outcome != "unknown" {
		t.Fatal(o)
	}
}
func httpCheck(url string) model.Check {
	return model.Check{ID: "check", Observer: "home", Kind: "http", Target: url, TimeoutSeconds: 1, HTTP: model.HTTPOptions{Method: "GET", MaxBodyBytes: 65536}}
}
