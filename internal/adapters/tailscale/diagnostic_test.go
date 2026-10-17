package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestTailscaleDiagnosticFixturesAndEvidence(t *testing.T) {
	for _, tc := range []struct{ file, protocol, route string }{{"direct.txt", "disco", "direct"}, {"derp.txt", "disco", "derp"}, {"peer-relay.txt", "disco", "peer_relay"}, {"tsmp.txt", "tsmp", "unknown"}} {
		raw, err := os.ReadFile(filepath.Join("testdata", "v1.90.0", tc.file))
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		command := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			calls++
			if name != "tailscale" {
				t.Fatal(name)
			}
			if args[0] == "status" {
				return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer","TailscaleIPs":["100.64.0.1"],"Online":true}}}`), nil
			}
			want := []string{"ping", "--c=1", "--until-direct=false", "--timeout=2s"}
			if tc.protocol == "tsmp" {
				want = append(want, "--tsmp")
			}
			want = append(want, "100.64.0.1")
			if !reflect.DeepEqual(args, want) {
				t.Fatal(args, want)
			}
			return raw, nil
		}
		o := model.Observation{Evidence: "reported", Facts: map[string]string{}}
		if err := Collect(context.Background(), command, model.TailscaleOptions{Peer: "peer", Diagnostic: tc.protocol}, &o); err != nil || o.Outcome != "ok" || o.Facts["diagnostic_route"] != tc.route || o.Evidence != "reported" || o.Connectivity || calls != 2 {
			t.Fatal(o, err, calls)
		}
		encoded, _ := json.Marshal(o)
		if strings.Contains(string(encoded), "private-node-name") {
			t.Fatal("raw diagnostic hostname exported")
		}
	}
}
func TestTailscaleDiagnosticsAreOptInAndDoNotResolveDNS(t *testing.T) {
	calls := 0
	cmd := func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer","Online":true}}}`), nil
	}
	o := model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), cmd, model.TailscaleOptions{Peer: "peer"}, &o); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	o = model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), cmd, model.TailscaleOptions{Peer: "peer", Diagnostic: "disco"}, &o); err != nil || calls != 2 || o.Code != "peer_diagnostic_ip_unavailable" {
		t.Fatal(o, err, calls)
	}
}
func TestTailscaleDiagnosticRejectsWrongPeerAndMalformedPong(t *testing.T) {
	target := netip.MustParseAddr("100.64.0.1")
	for _, raw := range []string{
		"pong from n (100.64.0.2) via DERP(hkg) in 2ms",
		"pong from n (100.64.0.1) via future-route in 2ms",
		"pong from n (100.64.0.1) via DERP(hkg) in -2ms",
		"pong from n (100.64.0.1) via DERP(hkg) in 100h",
		"pong from n (100.64.0.1) via DERP(hkg) in 2ms\nextra private text",
	} {
		if _, err := parsePing([]byte(raw), target, "disco"); err == nil {
			t.Fatal(raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	peer := peerStatus{TailscaleIPs: []string{"100.64.0.1"}}
	o := model.Observation{Outcome: "ok", Facts: map[string]string{}}
	if err := diagnose(ctx, func(context.Context, string, ...string) ([]byte, error) { return nil, context.Canceled }, model.TailscaleOptions{Diagnostic: "tsmp"}, peer, &o); err != nil || o.Outcome != "unknown" || o.Code != "diagnostic_canceled" {
		t.Fatal(o, err)
	}
	if err := diagnose(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("private raw error") }, model.TailscaleOptions{Diagnostic: "disco"}, peer, &o); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}
func FuzzTailscaleDiagnostic(f *testing.F) {
	f.Add([]byte("pong from n (100.64.0.1) via DERP(hkg) in 2ms"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		for _, mode := range []string{"disco", "tsmp"} {
			result, err := parsePing(raw, netip.MustParseAddr("100.64.0.1"), mode)
			if err == nil && (result.route == "" || result.latency < 0) {
				t.Fatal(result)
			}
		}
	})
}

func TestTailscaleDiagnosticPreservesOfflineAndHandlesDeadline(t *testing.T) {
	cmd := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer","TailscaleIPs":["100.64.0.1"],"Online":false}}}`), nil
		}
		return []byte("pong from n (100.64.0.1) via DERP(hkg) in 2ms"), nil
	}
	o := model.Observation{Evidence: "reported", Facts: map[string]string{}}
	if err := Collect(context.Background(), cmd, model.TailscaleOptions{Peer: "peer", Diagnostic: "disco"}, &o); err != nil || o.Code != "peer_offline" || o.Outcome != "unknown" || o.Connectivity || o.Evidence != "reported" {
		t.Fatal(o, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := diagnose(ctx, func(context.Context, string, ...string) ([]byte, error) { return nil, context.DeadlineExceeded }, model.TailscaleOptions{Diagnostic: "tsmp"}, peerStatus{TailscaleIPs: []string{"100.64.0.1"}}, &o); err != nil || o.Code != "diagnostic_timeout" || o.Outcome != "unknown" {
		t.Fatal(o, err)
	}
}

func TestTailscaleInternalTimeoutIsNotAMonitorError(t *testing.T) {
	timedOut := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer","TailscaleIPs":["100.64.0.1"],"Online":true}}}`), nil
		}
		return []byte("ping \"100.64.0.1\" timed out\n"), errors.New("exit status 1")
	}
	o := model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), timedOut, model.TailscaleOptions{Peer: "peer", Diagnostic: "disco"}, &o); err != nil {
		t.Fatal("an internal ping timeout was promoted to a monitor error", err)
	}
	if o.Outcome != "unknown" || o.Code != "diagnostic_ping_timed_out" || o.Facts["diagnostic_response"] != "timed_out" || o.Facts["diagnostic_route"] != "" {
		t.Fatalf("timeout not reported as no answer: %+v", o)
	}

	// Any other non-zero exit stays a monitor-side failure, even when the
	// CLI wrote something first.
	for _, raw := range []string{"no matching peer\n", "", "ping \"100.64.0.2\" timed out\n", "ping \"100.64.0.1\" timed out and then some\n", strings.Repeat(" ", 300) + "ping \"100.64.0.1\" timed out\n", "ping \"100.64.0.1\" timed out\x00"} {
		o := model.Observation{Facts: map[string]string{}}
		failing := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if args[0] == "status" {
				return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer","TailscaleIPs":["100.64.0.1"],"Online":true}}}`), nil
			}
			return []byte(raw), errors.New("exit status 1")
		}
		if err := Collect(context.Background(), failing, model.TailscaleOptions{Peer: "peer", Diagnostic: "disco"}, &o); err == nil {
			t.Fatalf("%q was treated as the peer's own timeout: %+v", raw, o)
		}
	}
}
