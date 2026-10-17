package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestTailscaleVersionedFixturesAndPeerRelay(t *testing.T) {
	for _, tc := range []struct{ file, identity, hint string }{{"status.json", "STABLE-a", ""}, {"status.json", "stable-a", "relay_preference_reported"}, {"status.json", "NODE-A.EXAMPLE.TS.NET", "relay_preference_reported"}, {"status.json", "fd7a:115c:a1e0:0:0:0:0:1", "relay_preference_reported"}, {"peer-relay.json", "stable-a", "peer_relay_reported"}} {
		raw, err := os.ReadFile(filepath.Join("testdata", "v1.90.0", tc.file))
		if err != nil {
			t.Fatal(err)
		}
		o := model.Observation{Facts: map[string]string{}}
		err = Collect(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return raw, nil }, model.TailscaleOptions{Peer: tc.identity}, &o)
		if err != nil {
			t.Fatal(err)
		}
		if tc.hint == "" {
			if o.Code != "peer_not_found" {
				t.Fatal(o)
			}
			continue
		}
		if o.Outcome != "ok" || o.Facts["transport_hint"] != tc.hint || o.Connectivity {
			t.Fatal(o)
		}
		encoded, _ := json.Marshal(o)
		if strings.Contains(string(encoded), "never-export") {
			t.Fatal("authentication metadata exported")
		}
	}
}
func TestTailscalePeerIdentityIsDeterministic(t *testing.T) {
	online := true
	peers := map[string]peerStatus{"key-a": {ID: "stable-a", HostName: "same", Online: &online}, "key-b": {ID: "stable-b", HostName: "same", Online: &online}}
	for i := 0; i < 100; i++ {
		if _, code := resolvePeer(peers, "same"); code != "ambiguous_peer" {
			t.Fatal(code)
		}
		if p, code := resolvePeer(peers, "stable-a"); code != "" || p.ID != "stable-a" {
			t.Fatal(p, code)
		}
	}
	peers["key-b"] = peerStatus{ID: "stable-b", HostName: "stable-a"}
	if p, code := resolvePeer(peers, "stable-a"); code != "" || p.ID != "stable-a" {
		t.Fatal("hostname shadowed stable ID", p, code)
	}
}
func TestTailscaleBackendAndMissingOnlineAreDistinct(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{`{"BackendState":"NeedsLogin","AuthURL":"private-auth","Peer":{}}`, "backend_needs_login"},
		{`{"BackendState":"NeedsMachineAuth","Peer":{}}`, "backend_needs_machine_auth"},
		{`{"BackendState":"Stopped","Peer":{}}`, "backend_stopped"},
		{`{"BackendState":"Starting","Peer":{}}`, "backend_not_ready"},
		{`{"BackendState":"future-secret","Peer":{}}`, "unsupported_backend_state"},
		{`{"BackendState":"Running","Peer":{"key":{"ID":"p"}}}`, "peer_online_unavailable"},
		{`{"BackendState":"Running","Peer":{"key":{"ID":"p","Online":false}}}`, "peer_offline"},
	} {
		o := model.Observation{Facts: map[string]string{}}
		err := Collect(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.body), nil }, model.TailscaleOptions{Peer: "p"}, &o)
		if err != nil || o.Outcome != "unknown" || o.Code != tc.code {
			t.Fatal(o, err)
		}
		raw, _ := json.Marshal(o)
		if strings.Contains(string(raw), "private-auth") || strings.Contains(string(raw), "future-secret") {
			t.Fatal(o)
		}
	}
}
func TestTailscaleResponseAndCommandBudgets(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"BackendState":"Running","BackendState":"Stopped"}`), []byte(strings.Repeat("x", (2<<20)+1)), []byte(`{"BackendState":"Running","ignored":` + strings.Repeat("[", 129) + `0` + strings.Repeat("]", 129) + `}`)} {
		if _, err := parseStatus(raw); err == nil {
			t.Fatal("hostile response accepted")
		}
	}
	for _, tc := range []struct {
		err  error
		code string
	}{{exec.ErrNotFound, "command_not_found"}, {os.ErrPermission, "command_permission_denied"}, {errors.New("private stderr"), "tailscale_unavailable_or_permission_denied"}} {
		err := Collect(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return nil, tc.err }, model.TailscaleOptions{Peer: "p"}, &model.Observation{Facts: map[string]string{}})
		var failure *adapters.Failure
		if !errors.As(err, &failure) || !failure.Local || failure.Code != tc.code {
			t.Fatal(err)
		}
	}
}
func FuzzTailscaleStatus(f *testing.F) {
	f.Add([]byte(`{"BackendState":"Running","Peer":{}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		status, err := parseStatus(raw)
		if err == nil {
			if status.BackendState == "" {
				t.Fatal("empty backend accepted")
			}
			resolvePeer(status.Peer, "peer")
		}
	})
}
