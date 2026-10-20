package cloudflare

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCollectTunnelFixture(t *testing.T) {
	o := observation()
	if err := collectTunnel(fixture(t, "tunnel-healthy.json"), tunnelOptions(), o); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"tunnel_status":            "healthy",
		"tunnel_connections":       "3",
		"tunnel_pending_reconnect": "1",
		"tunnel_colos":             "HKG,LAX,SIN",
		"connector_versions":       "2026.10.0,2026.9.1",
		"oldest_connection_at":     "2026-10-19T07:59:00Z",
	}
	for key, value := range want {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
		}
	}
	if o.Outcome != "ok" {
		t.Fatalf("outcome=%s", o.Outcome)
	}
	// Identifiers, the origin address, the synthetic name and the account tag
	// never become facts.
	raw, _ := json.Marshal(o.Facts)
	for _, forbidden := range []string{"192.0.2.10", "aaaaaaaa-aaaa", "synthetic-tunnel", testAccountID, "22222222"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("%q reached the facts: %s", forbidden, raw)
		}
	}
}

func TestCollectTunnelClosedStatus(t *testing.T) {
	for _, status := range []string{"healthy", "degraded", "down", "inactive"} {
		body := []byte(`{"success":true,"result":{"status":"` + status + `","connections":[]}}`)
		o := observation()
		if err := collectTunnel(body, tunnelOptions(), o); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if o.Facts["tunnel_status"] != status || o.Facts["tunnel_connections"] != "0" {
			t.Fatalf("%s: %+v", status, o.Facts)
		}
	}
	for _, body := range []string{
		`{"success":true,"result":{"status":"mystery","connections":[]}}`,
		`{"success":true,"result":{"connections":[]}}`,
		`{"success":true,"result":null}`,
		`{"success":true}`,
	} {
		if err := collectTunnel([]byte(body), tunnelOptions(), observation()); failureCode(err) != "unsupported_response_shape" {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

func TestCollectTunnelPolicies(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.CloudflareOptions)
		code   string
	}{
		{"declared connections", func(o *model.CloudflareOptions) { o.ExpectedConnections = intPtr(3) }, ""},
		{"below declared", func(o *model.CloudflareOptions) { o.ExpectedConnections = intPtr(4) }, "connections_below_expected"},
		{"declared colos", func(o *model.CloudflareOptions) { o.ExpectedColos = []string{"HKG", "LAX", "SIN"} }, ""},
		{"outside colo", func(o *model.CloudflareOptions) { o.ExpectedColos = []string{"HKG", "NRT"} }, "colo_outside_expected"},
		{"connections before colos", func(o *model.CloudflareOptions) {
			o.ExpectedConnections = intPtr(8)
			o.ExpectedColos = []string{"HKG"}
		}, "connections_below_expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := tunnelOptions()
			tc.mutate(&options)
			o := observation()
			if err := collectTunnel(fixture(t, "tunnel-healthy.json"), options, o); err != nil {
				t.Fatal(err)
			}
			if o.Code != tc.code {
				t.Fatalf("code=%q want %q", o.Code, tc.code)
			}
			if tc.code != "" && o.Outcome != "policy" {
				t.Fatalf("outcome=%s", o.Outcome)
			}
			if tc.code == "" && o.Outcome != "ok" {
				t.Fatalf("outcome=%s", o.Outcome)
			}
		})
	}
}

func TestCollectTunnelBoundsAndShapes(t *testing.T) {
	body := `{"success":true,"result":{"status":"healthy","connections":[` +
		`{"colo_name":"HKG","client_version":"2026.10.0","opened_at":"2026-10-19T08:00:00Z"},` +
		`{"colo_name":"SIN","client_version":"2026.9.1"},` +
		`{"colo_name":"LAX","client_version":"2026.8.0"},` +
		`{"colo_name":"NRT","client_version":"2026.7.0"},` +
		`{"colo_name":"SFO","client_version":"2026.6.0"},` +
		`{"colo_name":"SEA","client_version":"2026.5.0"},` +
		`{"colo_name":"ORD","client_version":"2026.4.0"},` +
		`{"colo_name":"IAD","client_version":"2026.3.0"},` +
		`{"colo_name":"JFK","client_version":"2026.2.0"},` +
		`{"colo_name":"AMS","client_version":"2026.1.0"},` +
		`{"colo_name":"lower","client_version":"not a version"},` +
		`{"colo_name":"123","client_version":"` + strings.Repeat("9", 40) + `"}` +
		`]}}`
	o := observation()
	if err := collectTunnel([]byte(body), tunnelOptions(), o); err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(o.Facts["tunnel_colos"], ","); len(got) != MaxTunnelColos {
		t.Fatalf("colos=%v", got)
	}
	if got := strings.Split(o.Facts["connector_versions"], ","); len(got) != MaxConnectionVersions {
		t.Fatalf("versions=%v", got)
	}
	if o.Facts["tunnel_connections"] != "12" {
		t.Fatalf("connections=%s", o.Facts["tunnel_connections"])
	}
	for _, fact := range []string{"tunnel_colos", "connector_versions"} {
		if strings.Contains(o.Facts[fact], "lower") || strings.Contains(o.Facts[fact], "123") || strings.Contains(o.Facts[fact], "not a version") {
			t.Fatalf("malformed token kept: %s=%s", fact, o.Facts[fact])
		}
	}
}

// The policy compares every distinct reported colo, including one beyond the
// eight the fact can carry: a ninth colo that sorts last must still be named
// as outside the declared list.
func TestCollectTunnelColoPolicyUsesFullSet(t *testing.T) {
	const connections = `[{"colo_name":"HKG"},{"colo_name":"IAD"},{"colo_name":"JFK"},{"colo_name":"LAX"},{"colo_name":"NRT"},{"colo_name":"ORD"},{"colo_name":"SEA"},{"colo_name":"SFO"},{"colo_name":"SIN"}]`
	body := `{"success":true,"result":{"status":"healthy","connections":` + connections + `}}`
	options := tunnelOptions()
	options.ExpectedColos = []string{"HKG", "IAD", "JFK", "LAX", "NRT", "ORD", "SEA", "SFO"}
	o := observation()
	if err := collectTunnel([]byte(body), options, o); err != nil {
		t.Fatal(err)
	}
	if o.Outcome != "policy" || o.Code != "colo_outside_expected" {
		t.Fatalf("ninth colo was not compared: %+v", o)
	}
	if got := strings.Split(o.Facts["tunnel_colos"], ","); len(got) != MaxTunnelColos || strings.Contains(o.Facts["tunnel_colos"], "SIN") {
		t.Fatalf("fact is not the capped list: %q", o.Facts["tunnel_colos"])
	}
}
