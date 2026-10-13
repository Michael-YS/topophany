package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/client"
)

func TestCredentialOverrideCannotReachFailoverDestination(t *testing.T) {
	var hits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(503) }))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(503) }))
	defer fallback.Close()
	path := filepath.Join(t.TempDir(), "credentials.json")
	c := client.Credentials{Schema: 1, Active: "test", Profiles: map[string]client.Profile{"test": {Project: "lab", Servers: []client.Endpoint{{URL: primary.URL, Token: strings.Repeat("a", 64)}, {URL: fallback.URL, Token: strings.Repeat("b", 64)}}}}}
	if err := client.Save(path, c); err != nil {
		t.Fatal(err)
	}
	_, _, err := invoke("", "--credentials", path, "--credential", strings.Repeat("c", 64), "--json")
	if err == nil || !strings.Contains(err.Error(), "--server") || hits.Load() != 0 {
		t.Fatal("unbound override made a request", err, hits.Load())
	}
	_, _, err = invoke("", "--credentials", path, "--server", primary.URL, "--credential", strings.Repeat("c", 64), "--json")
	if err == nil || hits.Load() != 1 {
		t.Fatal("explicit endpoint did not isolate failover", err, hits.Load())
	}
}
