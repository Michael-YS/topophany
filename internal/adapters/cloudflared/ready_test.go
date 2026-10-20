package cloudflared

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var testConnectorID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestParseReady(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		status  int
		ready   int
		id      string
		wantErr bool
	}{
		{name: "unready", body: `{"status":503,"readyConnections":0,"connectorId":"00000000-0000-4000-8000-000000000000"}`, status: 503, id: "00000000-0000-4000-8000-000000000000"},
		{name: "ready", body: `{"status":200,"readyConnections":4,"connectorId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}`, status: 200, ready: 4, id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"},
		{name: "uppercase uuid", body: `{"status":200,"readyConnections":1,"connectorId":"AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"}`, status: 200, ready: 1, id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"},
		{name: "not a uuid", body: `{"status":200,"readyConnections":1,"connectorId":"<script>alert(1)</script>"}`, status: 200, ready: 1},
		{name: "empty id", body: `{"status":200,"readyConnections":1}`, status: 200, ready: 1},
		{name: "unknown member", body: `{"status":200,"readyConnections":1,"extra":"ignored"}`, status: 200, ready: 1},
		{name: "whitespace", body: "\n {\"status\":503,\"readyConnections\":0} \n", status: 503},
		{name: "missing status", body: `{"readyConnections":1}`, wantErr: true},
		{name: "missing count", body: `{"status":200}`, wantErr: true},
		{name: "null", body: `null`, wantErr: true},
		{name: "array", body: `[{"status":200,"readyConnections":1}]`, wantErr: true},
		{name: "string values", body: `{"status":"200","readyConnections":"1"}`, wantErr: true},
		{name: "negative", body: `{"status":200,"readyConnections":-1}`, wantErr: true},
		{name: "absurd count", body: `{"status":200,"readyConnections":100000}`, wantErr: true},
		{name: "count at bound", body: `{"status":200,"readyConnections":1024}`, status: 200, ready: 1024},
		{name: "count over bound", body: `{"status":200,"readyConnections":1025}`, wantErr: true},
		{name: "status 599", body: `{"status":599,"readyConnections":1}`, status: 599, ready: 1},
		{name: "status range", body: `{"status":42,"readyConnections":1}`, wantErr: true},
		{name: "status 600", body: `{"status":600,"readyConnections":1}`, wantErr: true},
		{name: "duplicate key", body: `{"status":200,"status":503,"readyConnections":1}`, wantErr: true},
		{name: "trailing", body: `{"status":200,"readyConnections":1}extra`, wantErr: true},
		{name: "html", body: `<html>not here</html>`, wantErr: true},
		{name: "oversized", body: `{"status":200,"readyConnections":1,"padding":"` + strings.Repeat("x", ReadyBudget) + `"}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseReady([]byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("want error %v; got %v", tc.wantErr, err)
			}
			if err != nil {
				return
			}
			if got.Status != tc.status || got.Connections != tc.ready || got.ConnectorID != tc.id {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestParseReadyFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "2026.10.0", "ready-503.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseReady(body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != 503 || got.Connections != 0 || !testConnectorID.MatchString(got.ConnectorID) {
		t.Fatalf("live unready shape changed: %+v", got)
	}
}

func FuzzReady(f *testing.F) {
	for _, seed := range []string{
		`{"status":503,"readyConnections":0,"connectorId":"00000000-0000-4000-8000-000000000000"}`,
		`{"status":200,"readyConnections":4,"connectorId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}`,
		`{"status":200,"readyConnections":1,"connectorId":"not-a-uuid"}`,
		`null`, `[]`, `{"status":1e9}`, `{"status":200,"readyConnections":0,"connectorId":"\u0000"}`,
		strings.Repeat("x", ReadyBudget+1),
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := ParseReady(body)
		if err != nil {
			if len(body) <= ReadyBudget && got != (Ready{}) {
				t.Fatal("failed parse returned values")
			}
			return
		}
		if got.Status < 100 || got.Status > 599 || got.Connections < 0 || got.Connections > maxReadyConnections {
			t.Fatalf("unbounded values: %+v", got)
		}
		if got.ConnectorID != "" && (!testConnectorID.MatchString(got.ConnectorID) || strings.ToLower(got.ConnectorID) != got.ConnectorID) {
			t.Fatalf("identifier escaped its shape: %q", got.ConnectorID)
		}
	})
}
