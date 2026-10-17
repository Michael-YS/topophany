package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSnapshotHistoryPinsCollectorAcrossIndependentFailover(t *testing.T) {
	snap := snapshot()
	check := snap.Checks[0]
	var primaryHistory atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/snapshot" {
			w.WriteHeader(503)
			return
		}
		primaryHistory.Add(1)
		json.NewEncoder(w).Encode(History{Project: snap.Project, Revision: strings.Repeat("a", 64), Check: check.ID, Contract: strings.Repeat("b", 64)})
	}))
	defer primary.Close()
	var unavailable atomic.Bool
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/snapshot" {
			json.NewEncoder(w).Encode(snap)
			return
		}
		if unavailable.Load() {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(History{Project: snap.Project, Revision: snap.Revision, Check: check.ID, Contract: check.Contract})
	}))
	defer backup.Close()
	c := newClient(t, primary.URL, backup.URL)
	s, server, err := c.Snapshot(context.Background(), "")
	if err != nil || server != backup.URL {
		t.Fatal("snapshot did not fail over", server, err)
	}
	v := SnapshotHistory(s, server, check.ID)
	if _, got, err := c.HistoryAt(context.Background(), v, 5); err != nil || got != server || primaryHistory.Load() != 0 {
		t.Fatal("history did not stay with snapshot collector", got, err)
	}
	unavailable.Store(true)
	if _, _, err = c.HistoryAt(context.Background(), v, 5); !errors.Is(err, ErrNoCollector) || primaryHistory.Load() != 0 {
		t.Fatal("snapshot history fell through to another collector", err)
	}
	// The standalone history command retains its independent failover and source.
	if _, got, err := c.History(context.Background(), check.ID, 5); err != nil || got != primary.URL {
		t.Fatal("standalone history lost failover", got, err)
	}
}

func TestSnapshotHistoryRejectsChangedRevisionOrContract(t *testing.T) {
	snap := snapshot()
	check := snap.Checks[0]
	for _, kind := range []string{"revision", "contract", "project", "unconfigured source"} {
		t.Run(kind, func(t *testing.T) {
			h := History{Project: snap.Project, Revision: snap.Revision, Check: check.ID, Contract: check.Contract}
			switch kind {
			case "revision":
				h.Revision = strings.Repeat("a", 64)
			case "contract":
				h.Contract = strings.Repeat("b", 64)
			case "project":
				h.Project = "another-project"
			}
			server := jsonServer(t, 200, h)
			c := newClient(t, server.URL)
			v := SnapshotHistory(snap, server.URL, check.ID)
			if kind == "unconfigured source" {
				v.Server = "http://127.0.0.1:1"
			}
			if got, _, err := c.HistoryAt(context.Background(), v, 5); err == nil || len(got.Entries) != 0 {
				t.Fatal("mismatched history accepted", got, err)
			}
		})
	}
}
