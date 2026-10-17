package probe

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
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

func TestCommandCapturesOutputWhenItFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	want := `ping "192.0.2.1" timed out`
	out, err := runCaptured(exec.Command("sh", "-c", "printf '%s\n' 'ping \"192.0.2.1\" timed out'; exit 1"))
	if err == nil {
		t.Fatal("a failing command reported success")
	}
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("failed command output was lost: %q", out)
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

func TestTailscaleDiagnosticsHaveFixedBudgetAndLiteralTarget(t *testing.T) {
	for _, args := range [][]string{
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "100.64.0.1"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "--tsmp", "fd7a:115c:a1e0::1"},
	} {
		if !allowedCommand("tailscale", args) {
			t.Fatal("bounded diagnostic refused", args)
		}
	}
	for _, args := range [][]string{
		{"ping", "--c=0", "--until-direct=false", "--timeout=2s", "100.64.0.1"},
		{"ping", "--c=1", "--until-direct=true", "--timeout=2s", "100.64.0.1"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=1h", "100.64.0.1"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "--icmp", "100.64.0.1"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "example.invalid"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "127.0.0.1"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "::ffff:127.0.0.1"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "fe80::1%eth0"},
		{"ping", "--c=1", "--until-direct=false", "--timeout=2s", "--verbose"},
	} {
		if allowedCommand("tailscale", args) {
			t.Fatal("unbounded or unresolved diagnostic accepted", args)
		}
	}
}
