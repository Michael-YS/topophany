package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/alert"
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
	log := &lockedWriter{}
	a := &App{Err: log}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	a.watch(ctx, backend, ledger.Settle(c), nil)
	if !strings.Contains(log.String(), "incident opened: home/path/direct (") {
		t.Fatalf("collector log:\n%s", log.String())
	}
	written, e := ledger.Open(path)
	if e != nil || len(written.Records()) != 1 || written.Records()[0].State != "open" {
		t.Fatalf("ledger not written: %v", e)
	}
}

// With an alerts file, a collector tells its webhooks what it concludes:
// an incident that opens and, once settled, an observer with no evidence.
// A collector whose alerts file is missing or invalid does not start.
func TestCollectorAlertsOnWhatItConcludes(t *testing.T) {
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
	events := make(chan string, 8)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct{ Event string }
		json.NewDecoder(r.Body).Decode(&m)
		events <- m.Event
	}))
	defer hook.Close()
	backend := api.New(c, "primary", st, api.Auth{})
	backend.Ledger = ledger.New()
	log := &lockedWriter{}
	notifier := &alert.Notifier{Project: c.Project, Instance: "primary", Log: func(f string, args ...any) { fmt.Fprintf(log, f+"\n", args...) }}
	notifier.Configure(alert.Config{Schema: 1, Webhooks: []alert.Webhook{{ID: "hook", Format: "json", URL: hook.URL}}})
	defer notifier.Close()
	a := &App{Err: log}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	a.watch(ctx, backend, 0, notifier)
	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case e := <-events:
			got[e] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("alerts sent: %v\n%s", got, log.String())
		}
	}
	if !got["incident.opened"] || !got["observer.silent"] {
		t.Fatalf("alerts sent: %v", got)
	}
	initDir, _, _, _ := initialized(t)
	for _, alerts := range []string{filepath.Join(initDir, "missing.json"), filepath.Join(initDir, "topology.json")} {
		_, e := at(time.Now(), "serve", "--config", filepath.Join(initDir, "topology.json"), "--auth", filepath.Join(initDir, "auth.json"), "--alerts", alerts, "--data", filepath.Join(initDir, "data"), "--listen", "127.0.0.1:0")
		if e == nil || !strings.Contains(e.Error(), alerts) {
			t.Fatalf("serve with alerts %s: %v", alerts, e)
		}
	}
}
