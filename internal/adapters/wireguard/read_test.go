package wireguard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func fixtureCommand(t *testing.T, replacements map[string]string) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "wg" || len(args) != 3 || args[0] != "show" || args[1] != "wg0" {
			t.Fatal(name, args)
		}
		field := args[2]
		if raw, ok := replacements[field]; ok {
			return []byte(raw), nil
		}
		switch field {
		case "latest-handshakes", "transfer", "allowed-ips", "persistent-keepalive":
			raw, err := os.ReadFile(filepath.Join("testdata", "v1.0.20210914", field+".tsv"))
			return raw, err
		default:
			t.Fatal("sensitive field requested", field)
			return nil, nil
		}
	}
}

func TestWireGuardPublicFieldFixturesAndPolicies(t *testing.T) {
	keepalive := 25
	options := model.WireGuardOptions{Interface: "wg0", Peer: "PUBLIC-PEER", ExpectedAllowedIPs: []string{"::/0", "10.0.0.0/24"}, ExpectedKeepaliveSeconds: &keepalive}
	o := model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), fixtureCommand(t, nil), options, &o); err != nil || o.Outcome != "ok" || o.Connectivity {
		t.Fatal(o, err)
	}
	if o.Facts["allowed_ips_match"] != "true" || o.Facts["keepalive_match"] != "true" {
		t.Fatal(o)
	}
	keepalive = 0
	options.ExpectedAllowedIPs = []string{"192.0.2.0/24"}
	o = model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), fixtureCommand(t, nil), options, &o); err != nil || o.Outcome != "policy" || o.Code != "keepalive_mismatch" || o.Facts["allowed_ips_match"] != "false" {
		t.Fatal(o, err)
	}
	options.ExpectedAllowedIPs = nil
	o = model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), fixtureCommand(t, map[string]string{"persistent-keepalive": "PUBLIC-PEER\toff\n"}), options, &o); err != nil || o.Outcome != "ok" {
		t.Fatal(o, err)
	}
}

func TestWireGuardMalformedDataAndChangingPeerAreDistinct(t *testing.T) {
	options := model.WireGuardOptions{Interface: "wg0", Peer: "PUBLIC-PEER"}
	for _, raw := range []string{"PUBLIC-PEER\n", "PUBLIC-PEER\t100\textra\n", "PUBLIC-PEER\t100\nPUBLIC-PEER\t100\n", "PUBLIC-PEER\t-1\n", "PUBLIC-PEER\t9223372036854775807\n"} {
		o := model.Observation{Facts: map[string]string{}}
		if err := Collect(context.Background(), fixtureCommand(t, map[string]string{"latest-handshakes": raw}), options, &o); err == nil {
			t.Fatal("malformed data treated as absent peer", raw, o)
		}
	}
	for _, raw := range []string{"PUBLIC-PEER\t-1\t0\n", "PUBLIC-PEER\t18446744073709551616\t0\n"} {
		if err := Collect(context.Background(), fixtureCommand(t, map[string]string{"transfer": raw}), options, &model.Observation{Facts: map[string]string{}}); err == nil {
			t.Fatal("invalid counter accepted")
		}
	}
	o := model.Observation{Facts: map[string]string{}}
	if err := Collect(context.Background(), fixtureCommand(t, map[string]string{"transfer": ""}), options, &o); err != nil || o.Code != "peer_snapshot_changed" || o.Outcome != "unknown" {
		t.Fatal(o, err)
	}
	encoded, _ := json.Marshal(o)
	if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "PRESHARED") {
		t.Fatal("unexpected secret field")
	}
	if _, err := parseRows([]byte(strings.Repeat("x", (2<<20)+1)), 2); err == nil {
		t.Fatal("response budget bypassed")
	}
}

func TestWireGuardPolicyValidation(t *testing.T) {
	for _, iface := range []string{"all", "interfaces", "--help", "wg0;true"} {
		if Validate(model.WireGuardOptions{Interface: iface, Peer: "peer"}) == nil {
			t.Fatal(iface)
		}
	}
	for _, cidrs := range [][]string{{}, {"bad"}, {"10.0.0.0/24", "10.0.0.1/24"}} {
		if Validate(model.WireGuardOptions{Interface: "wg0", Peer: "peer", ExpectedAllowedIPs: cidrs}) == nil {
			t.Fatal(cidrs)
		}
	}
}

func FuzzWireGuardRows(f *testing.F) {
	f.Add([]byte("peer\t100\n"))
	f.Add([]byte("peer\t100\t200\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		for _, columns := range []int{2, 3} {
			rows, err := parseRows(raw, columns)
			if err == nil {
				for key, row := range rows {
					if key == "" || len(row) != columns-1 {
						t.Fatal("invalid row accepted")
					}
				}
			}
		}
	})
}

func TestWireGuardKeepaliveRange(t *testing.T) {
	for _, value := range []int{-1, 65536} {
		if Validate(model.WireGuardOptions{Interface: "wg0", Peer: "peer", ExpectedKeepaliveSeconds: &value}) == nil {
			t.Fatal(value)
		}
	}
}
