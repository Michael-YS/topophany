package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestSnapshotViewsPinsCompanionAfterFailover(t *testing.T) {
	c := testkit.Config()
	var injected atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("observer") != "" {
			w.WriteHeader(503)
			return
		}
		injected.Add(1)
		json.NewEncoder(w).Encode(engine.Build(c, "malicious", "", nil, nil, time.Now()))
	}))
	defer first.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(engine.Build(c, "backup", r.URL.Query().Get("observer"), nil, nil, time.Now()))
	}))
	defer backup.Close()
	cl := newClient(t, first.URL, backup.URL)
	defer cl.Close()
	scoped, all, err := cl.SnapshotViews(context.Background(), "home")
	if err != nil || all == nil || scoped.Instance != "backup" || all.Instance != "backup" || injected.Load() != 0 {
		t.Fatal("mixed collector evidence", err, scoped.Instance, all, injected.Load())
	}
}

func TestSnapshotViewsRetriesTheWholeGeneration(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(map[bool]string{true: "reject", false: "retry"}[permanent], func(t *testing.T) {
			var reads atomic.Int32
			c := testkit.Config()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := reads.Add(1)
				s := engine.Build(c, "collector", r.URL.Query().Get("observer"), nil, nil, time.Now())
				if n == 1 || permanent && n%2 == 1 {
					s.Revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				}
				json.NewEncoder(w).Encode(s)
			}))
			defer server.Close()
			cl := newClient(t, server.URL)
			defer cl.Close()
			s, all, err := cl.SnapshotViews(context.Background(), "home")
			if permanent {
				if err == nil || all != nil {
					t.Fatal("accepted mixed generation")
				}
			} else if err != nil || all == nil || all.Revision != s.Revision {
				t.Fatal("did not retry complete pair", err)
			}
			if reads.Load() != 4 {
				t.Fatal("unexpected retry count", reads.Load())
			}
		})
	}
}
