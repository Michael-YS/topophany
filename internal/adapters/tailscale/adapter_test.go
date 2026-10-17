package tailscale_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestTailscaleIdleOfflineAndScope(t *testing.T) {
	for _, tt := range []struct{ name, online, active, cur, relay, want, hint string }{{"idle is not offline", "true", "false", "127.0.0.1:41641", "", "ok", "direct_endpoint_reported"}, {"DERP preference is not request proof", "true", "true", "", "hkg", "ok", "relay_preference_reported"}, {"offline report not power state", "false", "false", "", "", "unknown", "unknown"}} {
		t.Run(tt.name, func(t *testing.T) {
			r := probe.Runner{Command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name != "tailscale" || !reflect.DeepEqual(args, []string{"status", "--json"}) {
					t.Fatal("unexpected command", name, args)
				}
				return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer-1","HostName":"dmit","TailscaleIPs":["100.64.0.1"],"Online":` + tt.online + `,"Active":` + tt.active + `,"CurAddr":"` + tt.cur + `","Relay":"` + tt.relay + `"}}}`), nil
			}}
			x := httpCheck("")
			x.Kind = "tailscale"
			x.Adapter.Peer = "100.64.0.1"
			o := r.Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.want || o.Evidence != "reported" || o.Facts["transport_hint"] != tt.hint || o.Connectivity {
				t.Fatal(o)
			}
		})
	}
	x := httpCheck("")
	x.Kind = "tailscale"
	x.Adapter.Peer = "missing"
	r := probe.Runner{Command: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Peer":{}}`), nil
	}}
	if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "unknown" || o.Code != "peer_not_found" {
		t.Fatal(o)
	}
	r.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("secret raw stderr") }
	o := r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || strings.Contains(o.Detail, "secret") {
		t.Fatal(o)
	}
}
func httpCheck(url string) model.Check {
	return model.Check{ID: "check", Observer: "home", Kind: "http", Target: url, TimeoutSeconds: 1, HTTP: model.HTTPOptions{Method: "GET", MaxBodyBytes: 65536}}
}

func TestTailscaleMissingLastSeenIsNotTheYearOne(t *testing.T) {
	r := probe.Runner{Command: func(context.Context, string, ...string) ([]byte, error) {
		// A peer the control plane reports as online carries no LastSeen,
		// which the zero time would render as 0001-01-01.
		return []byte(`{"BackendState":"Running","Peer":{"key":{"ID":"peer-1","TailscaleIPs":["100.64.0.1"],"Online":true}}}`), nil
	}}
	x := httpCheck("")
	x.Kind = "tailscale"
	x.Adapter.Peer = "100.64.0.1"
	o := r.Run(context.Background(), testkit.Config(), x)
	if got := o.Facts["last_seen"]; got != "not reported" {
		t.Fatalf("missing LastSeen rendered as %q", got)
	}
	for _, fact := range o.Facts {
		if strings.Contains(fact, "0001-01-01") {
			t.Fatalf("a fabricated time reached the facts: %v", o.Facts)
		}
	}
}
