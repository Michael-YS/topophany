package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const remoteViewerToken = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestRemoteHistoryStaysWithSnapshotCollector(t *testing.T) {
	backup, p := remoteFixture(t)
	var wrongHistory atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/history" {
			wrongHistory.Add(1)
		}
		w.WriteHeader(503)
	}))
	defer primary.Close()
	p.Servers = append([]client.Endpoint{{URL: primary.URL, Token: remoteViewerToken}}, p.Servers...)
	r, err := NewRemote(p, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	waitRemote(t, r, func() bool { return !r.m.Snapshot.GeneratedAt.IsZero() })
	r.Key("6", time.Now())
	selectFirstEntry(t, r.m)
	waitRemote(t, r, func() bool { return len(r.m.samples.entries) > 0 })
	if r.m.Server != backup.URL || wrongHistory.Load() != 0 {
		t.Fatal("remote history restarted collector failover")
	}
}

func remoteFixture(t *testing.T) (*httptest.Server, client.Profile) {
	t.Helper()
	return remoteFixtureClock(t, time.Now)
}

func remoteFixtureClock(t *testing.T, now func() time.Time) (*httptest.Server, client.Profile) {
	t.Helper()
	c := testkit.Config()
	st, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, now().Add(-time.Second), "ok")}}); err != nil {
		t.Fatal(err)
	}
	backend := api.New(c, "real-collector", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(remoteViewerToken), Role: "viewer"}}})
	backend.Now = now
	srv := httptest.NewServer(backend.Handler())
	t.Cleanup(srv.Close)
	return srv, client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: srv.URL, Token: remoteViewerToken}}}
}

func waitRemote(t *testing.T, r *Remote, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.Frame(time.Now(), 120, 36, false)
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("remote viewer did not reach expected state", r.m.Error)
}

func TestRemoteReadsRealCollectorViewsAndHistory(t *testing.T) {
	_, p := remoteFixture(t)
	r, err := NewRemote(p, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	waitRemote(t, r, func() bool { return !r.m.Snapshot.GeneratedAt.IsZero() })
	if r.m.Preview != nil || r.m.Snapshot.Project != p.Project || r.m.Snapshot.Instance != "real-collector" {
		t.Fatal("real viewer used preview data")
	}
	r.Key("v", time.Now())
	waitRemote(t, r, func() bool { return !r.m.Snapshot.GeneratedAt.IsZero() && r.m.Snapshot.ViewObserver == r.m.Observer })
	r.Key("6", time.Now())
	selectFirstEntry(t, r.m)
	waitRemote(t, r, func() bool { return len(r.m.samples.entries) > 0 })
	if r.m.samples.entries[0].Check != "home-http" {
		t.Fatal("wrong sample history")
	}
}

func TestRemoteRefusesWrongProjectAndTokenWithoutPreviewFallback(t *testing.T) {
	_, profile := remoteFixture(t)
	for _, kind := range []string{"project", "token"} {
		t.Run(kind, func(t *testing.T) {
			p := client.Profile{Project: profile.Project, Servers: append([]client.Endpoint(nil), profile.Servers...)}
			if kind == "project" {
				p.Project = "another-project"
			} else {
				p.Servers[0].Token = strings.Repeat("d", 64)
			}
			r, err := NewRemote(p, "")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			waitRemote(t, r, func() bool { return r.m.Error != "" })
			if !r.m.Snapshot.GeneratedAt.IsZero() || r.m.Preview != nil || strings.Contains(r.m.Error, p.Servers[0].Token) {
				t.Fatal("rejected connection exposed data or substituted demo")
			}
		})
	}
}

func TestRemoteDisconnectKeepsCachedEvidence(t *testing.T) {
	srv, p := remoteFixture(t)
	r, err := NewRemote(p, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	waitRemote(t, r, func() bool { return !r.m.Snapshot.GeneratedAt.IsZero() })
	before := r.m.Snapshot.GeneratedAt
	srv.Close()
	r.Key("r", time.Now())
	waitRemote(t, r, func() bool { return r.m.Error != "" })
	if !r.m.Snapshot.GeneratedAt.Equal(before) || !r.m.Cached() {
		t.Fatal("disconnect lost or refreshed old evidence")
	}
}

func TestRemoteSnapshotBusyDoesNotFlashOffline(t *testing.T) {
	for _, delta := range []time.Duration{2 * time.Second, -2 * time.Second} {
		t.Run(delta.String(), func(t *testing.T) {
			// GeneratedAt is wall time, not a monotonic refresh counter. A real
			// clock correction reproduced a successful retry with an earlier stamp.
			start := time.Now().UTC()
			var offset atomic.Int64
			clock := func() time.Time { return start.Add(time.Duration(offset.Load())) }
			base, p := remoteFixtureClock(t, clock)
			var busy atomic.Bool
			var busyReplies, requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/v1/snapshot" {
					requests.Add(1)
					if busy.CompareAndSwap(true, false) {
						busyReplies.Add(1)
						w.Header().Set("Retry-After", "1")
						w.WriteHeader(429)
						_, _ = w.Write([]byte(`{"error":"snapshot_busy"}`))
						return
					}
				}
				base.Config.Handler.ServeHTTP(w, req)
			}))
			defer srv.Close()
			p.Servers[0].URL = srv.URL
			r, err := NewRemote(p, "")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			r.client.Now = clock
			pump := func(expected time.Time) {
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					// Fix the display clock too, so a delayed test runner cannot start
					// unrelated periodic reads while we assert this explicit refresh.
					r.Frame(clock(), 120, 36, false)
					if r.m.Error != "" || !r.m.Snapshot.GeneratedAt.IsZero() && r.m.Cached() {
						t.Fatal("one busy snapshot flashed offline", r.m.Error)
					}
					if r.m.Snapshot.GeneratedAt.Equal(expected) {
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
				t.Fatalf("snapshot not received: want=%v got=%v requests=%d", expected, r.m.Snapshot.GeneratedAt, requests.Load())
			}
			pump(start)
			offset.Store(int64(delta))
			busy.Store(true)
			r.Key("r", clock())
			pump(start.Add(delta))
			if busyReplies.Load() != 1 || requests.Load() != 3 {
				t.Fatalf("expected initial/busy/retry reads, got requests=%d busy=%d", requests.Load(), busyReplies.Load())
			}
		})
	}
}

func TestRemotePendingReadDoesNotBlockKeysAndQuitCancels(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer srv.Close()
	r, err := NewRemote(client.Profile{Project: "test", Servers: []client.Endpoint{{URL: srv.URL, Token: remoteViewerToken}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	start := time.Now()
	r.Frame(start, 120, 36, true)
	if time.Since(start) > time.Second {
		t.Fatal("frame blocked on collector")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("read not started")
	}
	r.Key("3", time.Now())
	r.Frame(time.Now(), 120, 36, true)
	if r.m.Tab != tabServers || r.m.Loading == "" {
		t.Fatal("pending read blocked navigation")
	}
	r.Key("q", time.Now())
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("quit did not cancel network request")
	}
	if r.ctx.Err() != context.Canceled {
		t.Fatal("session context still alive")
	}
}
