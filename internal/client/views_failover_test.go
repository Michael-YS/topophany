package client

import (
	"context"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScopedPairFailsOverAsAUnit(t *testing.T) {
	c := testkit.Config()
	var backupCalls atomic.Int32
	serve := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(engine.Build(c, "audit", r.URL.Query().Get("observer"), nil, nil, time.Now()))
	}
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("observer") == "" {
			w.WriteHeader(503)
			return
		}
		serve(w, r)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backupCalls.Add(1); serve(w, r) }))
	defer second.Close()
	cl, e := New(Profile{Project: c.Project, Servers: []Endpoint{{URL: first.URL, Token: strings.Repeat("a", 64)}, {URL: second.URL, Token: strings.Repeat("a", 64)}}})
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	_, _, _, e = cl.SourcedViews(context.Background(), "home")
	if e != nil {
		t.Fatalf("healthy backup ignored: calls=%d error=%v", backupCalls.Load(), e)
	}
}
