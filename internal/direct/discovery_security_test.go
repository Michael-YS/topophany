package direct

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryRejectsPasswordInObjectIdentity(t *testing.T) {
	const password = "password1234567"
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "auth-with-password") {
			fmt.Fprint(w, `{"token":"synthetic-token-independent","record":{"id":"uuuuuuuuuuuuuuu","collectionName":"users"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "perPage": 100, "totalPages": 1, "totalItems": 1, "items": []System{{ID: password, Name: password}}})
	}))
	defer hub.Close()
	h, err := NewHub(SourceConfig{ID: "audit", Name: "Audit", Adapter: "beszel", URL: hub.URL, Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	c, err := h.authenticate(context.Background(), Auth{Mode: "password", Identity: "audit@example.invalid", Password: password}, nil)
	if err != nil {
		t.Fatal(err)
	}
	systems, _, err := h.Discover(context.Background(), c)
	if err != nil {
		return
	}
	wire, _ := json.Marshal(systems)
	if strings.Contains(string(wire), password) {
		t.Fatalf("password escaped through public discovery: %s", wire)
	}
}
