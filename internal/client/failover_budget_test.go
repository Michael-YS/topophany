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

func TestMonitorDeadlineReservesBackupAttempt(t *testing.T) {
	c := testkit.Config()
	var backupCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls.Add(1)
		_ = json.NewEncoder(w).Encode(engine.Build(c, "audit", "", nil, nil, time.Now()))
	}))
	defer second.Close()
	cl, e := New(Profile{Project: c.Project, Servers: []Endpoint{{URL: first.URL, Token: strings.Repeat("a", 64)}, {URL: second.URL, Token: strings.Repeat("a", 64)}}})
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _, e = cl.SourcedViews(ctx, "")
	if e != nil {
		t.Fatalf("monitor budget consumed by primary: backup calls=%d error=%v", backupCalls.Load(), e)
	}
}
