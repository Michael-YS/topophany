package probe

import (
	"context"
	"errors"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestAdapterExecutableAndOutputBudget(t *testing.T) {
	if _, e := defaultCommand(context.Background(), "sh", "-c", "anything"); e == nil {
		t.Fatal("arbitrary command accepted")
	}
	w := &cappedOutput{limit: 3}
	if _, e := w.Write([]byte("abc")); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Write([]byte("x")); e == nil {
		t.Fatal("command output unbounded")
	}
	x := httpCheck("")
	x.Kind = "wireguard"
	x.Adapter = model.AdapterOptions{Interface: "wg0", Peer: "peer"}
	r := Runner{Command: func(context.Context, string, ...string) ([]byte, error) { return []byte("bad"), nil }}
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "error" {
		t.Fatal(o)
	}
	r.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no permission") }
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "error" {
		t.Fatal(o)
	}
}

func TestSoftwareCommandsAreReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"wg", []string{"set", "wg0", "private-key", "/tmp/key"}},
		{"wg", []string{"show", "wg0", "dump"}},
		{"wg", []string{"show", "wg0", "private-key"}},
		{"wg", []string{"show", "all", "transfer"}},
		{"tailscale", []string{"up"}},
		{"tailscale", []string{"set", "--exit-node=example"}},
	} {
		if allowedCommand(tc.name, tc.args) {
			t.Fatal(tc)
		}
	}
	if !allowedCommand("wg", []string{"show", "wg0", "transfer"}) || !allowedCommand("tailscale", []string{"status", "--json"}) {
		t.Fatal("valid read refused")
	}
}
