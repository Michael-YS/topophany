package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A collector judges its ledger on its own, whether anyone reads it or
// not: an incident confirmed in its journal is logged and written down.
func TestCollectorKeepsItsLedgerUnread(t *testing.T) {
	c := testkit.Config()
	dir := t.TempDir()
	st, e := store.Open(dir, store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Observers: observerIDs(c)})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	now := time.Now().UTC()
	b := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c)}
	for i := uint64(1); i <= 3; i++ {
		b.Observations = append(b.Observations, testkit.Observation(c, "home-http", i, now.Add(-time.Duration(4-i)*time.Second), "fail"))
	}
	if e = st.Put(b); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "incidents.json")
	book, e := ledger.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	backend := api.New(c, "primary", st, api.Auth{})
	backend.Ledger = book
	var log syncBuffer
	a := &App{Err: &log}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	a.watch(ctx, backend, ledger.Settle(c))
	if !strings.Contains(log.String(), "incident opened: home/path/direct (") {
		t.Fatalf("collector log:\n%s", log.String())
	}
	written, e := ledger.Open(path)
	if e != nil || len(written.Records()) != 1 || written.Records()[0].State != "open" {
		t.Fatalf("ledger not written: %v", e)
	}
}

// syncBuffer is a log written from one goroutine and read from another.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
