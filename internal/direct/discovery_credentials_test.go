package direct

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryRedactsResolvedAndLongCredentials(t *testing.T) {
	for _, mode := range []string{"saved", "env", "env-refreshed", "password", "token"} {
		t.Run(mode, func(t *testing.T) {
			old := "old-synthetic-private-token-canary"
			secret := old
			auth := Auth{Mode: mode}
			saved := &Credential{Mode: "token", Token: old, Subject: "users:" + testSubject}
			if strings.HasPrefix(mode, "env") {
				auth.Mode = "env"
				t.Setenv("TOPO_AUDIT_PRIVATE_TOKEN", old)
				auth.Name = "TOPO_AUDIT_PRIVATE_TOKEN"
			}
			if mode == "password" {
				secret = strings.Repeat("p", 256)
				auth.Identity = "audit@example.invalid"
				auth.Password = secret
			}
			if mode == "token" {
				auth.Token = strings.Repeat("t", 256)
				secret = auth.Token
			}
			if mode == "env-refreshed" {
				secret = "new-synthetic-private-token-canary"
			}
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "auth-") {
					json.NewEncoder(w).Encode(map[string]any{"token": "new-synthetic-private-token-canary", "record": map[string]string{"id": testSubject, "collectionName": "users"}})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"page": 1, "perPage": 100, "totalPages": 1, "totalItems": 1, "items": []System{{ID: testObject, Name: secret}}})
			}))
			defer hub.Close()
			h, err := NewHub(SourceConfig{ID: "audit", Name: "Audit", Adapter: "beszel", URL: hub.URL, Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			c, err := h.authenticate(context.Background(), auth, saved)
			if err != nil {
				t.Fatal(err)
			}
			systems, _, err := h.Discover(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			wire, _ := json.Marshal(systems)
			prefix := secret[:min(128, len(secret))]
			if strings.Contains(string(wire), prefix) {
				t.Fatalf("credential reflected, mode=%s, public name length=%d", mode, len(systems[0].Name))
			}
		})
	}
}

func TestDiscoveryNameChecksSecretsBeforeTruncation(t *testing.T) {
	for _, n := range []int{127, 128, 129, 256} {
		secret := strings.Repeat("s", n)
		if got := (discoverySecrets{"unrelated", secret}).cleanName(secret, testObject); got != "System "+testObject {
			t.Errorf("length=%d leaked name %q", n, got)
		}
	}
	if got := (discoverySecrets{"password"}).cleanName("pass\x1bword", testObject); got != "System "+testObject {
		t.Errorf("sanitized secret leaked: %q", got)
	}
}
