package wireguard_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestWireGuardOldHandshakeIsNotFailureAndKeysAreDiscarded(t *testing.T) {
	x := httpCheck("")
	x.Kind = "wireguard"
	x.Adapter = model.AdapterOptions{Interface: "wg0", Peer: "PUBLIC-PEER"}
	r := probe.Runner{Command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "wg" || !reflect.DeepEqual(args, []string{"show", "wg0", "dump"}) {
			t.Fatal("unexpected command", name, args)
		}
		return []byte("INTERFACE-PRIVATE-KEY interface-public 51820 off\nPUBLIC-PEER PRESHARED-SECRET 192.0.2.1:51820 10.0.0.0/24 100 123 456 0\n"), nil
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
