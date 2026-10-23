package komari

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/source"
)

func TestDiscoveryReadOnlyAuthenticationAndProjection(t *testing.T) {
	for _, anonymous := range []bool{false, true} {
		s := testSource()
		body := fmt.Sprintf(`[{"uuid":%q,"name":%q,"token":"private-agent-token","ipv4":"192.0.2.1"},{"uuid":%q,"name":"Visible"}]`, testObject, "name "+testToken, otherObject)
		path := "/api/admin/client/list"
		if anonymous {
			s.CredentialEnv = ""
			body = `{"status":"success","data":` + body + `}`
			path = "/api/nodes"
		}
		io := &fixtureIO{token: testToken, responses: []source.Response{{Status: 200, ContentType: "application/json", Body: []byte(body)}}}
		nodes, err := Discover(context.Background(), io, s)
		if err != nil || len(nodes) != 2 || nodes[0].ID != otherObject || len(io.gets) != 1 || io.gets[0].target != s.Target+path {
			t.Fatalf("anonymous=%v nodes=%v err=%v reads=%v", anonymous, nodes, err, io.gets)
		}
		encoded, _ := json.Marshal(nodes)
		if strings.Contains(string(encoded), "private-agent-token") || strings.Contains(string(encoded), "192.0.2.1") || !anonymous && strings.Contains(string(encoded), testToken) {
			t.Fatal("discovery exposed private upstream fields")
		}
		if anonymous {
			if io.credentials != 0 || io.gets[0].headers["Authorization"] != "" {
				t.Fatal("anonymous discovery resolved or sent a credential")
			}
		} else if io.credentials != 1 || io.gets[0].headers["Authorization"] != "Bearer "+testToken {
			t.Fatal("missing explicit Bearer authentication")
		}
	}
}

func TestDiscoveryRejectsInvalidResponsesWithoutFallback(t *testing.T) {
	valid := fmt.Sprintf(`{"uuid":%q,"name":"Node"}`, testObject)
	for _, body := range []string{
		`null`, `{}`, `{"status":"success","data":[]}`, `[{"uuid":"bad"}]`,
		`[` + valid + `,` + valid + `]`, `[{"uuid":"bad","uuid":"` + testObject + `"}]`,
		`[{"UUID":"` + testObject + `"}]`, `[{"uuid":"` + testObject + `","name":42}]`,
		`[{"uuid":"` + strings.ToUpper(testObject) + `"}]`,
	} {
		io := &fixtureIO{token: testToken, responses: []source.Response{{Status: 200, Body: []byte(body)}}}
		if nodes, err := Discover(context.Background(), io, testSource()); err == nil || nodes != nil || len(io.gets) != 1 {
			t.Fatalf("accepted malformed listing %s", body)
		}
	}
	for _, status := range []int{401, 403, 404, 429, 500, 302} {
		io := &fixtureIO{token: testToken, responses: []source.Response{{Status: status}}}
		if nodes, err := Discover(context.Background(), io, testSource()); err == nil || nodes != nil || len(io.gets) != 1 {
			t.Fatalf("status %d was accepted or retried", status)
		}
	}
	io := &fixtureIO{token: testToken}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, io, testSource()); source.Code(err) != "source_canceled" || len(io.gets) != 0 {
		t.Fatal("canceled discovery performed a read", err)
	}
	io = &fixtureIO{token: testToken, pages: 2}
	if _, err := Discover(context.Background(), io, testSource()); source.Code(err) != "page_budget" || io.credentials != 0 || len(io.gets) != 0 {
		t.Fatal("discovery exceeded its page budget", err)
	}
}

func TestDiscoveryEmptyListingAndNodeBudget(t *testing.T) {
	for _, anonymous := range []bool{false, true} {
		s := testSource()
		body := `[]`
		if anonymous {
			s.CredentialEnv = ""
			body = `{"status":"success","data":[]}`
		}
		io := &fixtureIO{token: testToken, responses: []source.Response{{Status: 200, Body: []byte(body)}}}
		if nodes, err := Discover(context.Background(), io, s); err != nil || nodes == nil || len(nodes) != 0 {
			t.Fatal("valid empty listing failed", err)
		}
	}
	for _, count := range []int{1000, 1001} {
		records := make([]string, count)
		for i := range records {
			records[i] = fmt.Sprintf(`{"uuid":"%08x-0000-0000-0000-000000000000","name":"Synthetic"}`, i)
		}
		io := &fixtureIO{token: testToken, responses: []source.Response{{Status: 200, Body: []byte("[" + strings.Join(records, ",") + "]")}}}
		nodes, err := Discover(context.Background(), io, testSource())
		if count == 1000 && (err != nil || len(nodes) != count) {
			t.Fatal("bounded listing failed", err)
		}
		if count == 1001 && (source.Code(err) != "komari_discovery_budget" || nodes != nil) {
			t.Fatal("oversized listing was truncated or accepted", err)
		}
	}
}
