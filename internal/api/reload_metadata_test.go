package api

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestReloadCannotExposeNewMetadataToOldViewer(t *testing.T) {
	srv, h, _ := fixture(t)
	now := srv.Now()
	srv.Ledger = ledger.New()
	if w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil); w.Code != 200 {
		t.Fatal("could not warm the old generation cache", w.Code)
	}
	admitted, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var barrier, unblock sync.Once
	release := func() { unblock.Do(func() { close(resume) }) }
	t.Cleanup(func() {
		release()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("admitted request did not finish")
		}
	})
	// allow reads the clock after admit has captured its immutable authority.
	srv.Now = func() time.Time {
		barrier.Do(func() { close(admitted); <-resume })
		return now
	}
	r := httptest.NewRequest("GET", "/v1/snapshot", nil)
	r.Header.Set("Authorization", "Bearer "+viewerToken)
	w := httptest.NewRecorder()
	go func() { defer close(done); h.ServeHTTP(w, r) }()
	select {
	case <-admitted:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach the post-authentication barrier")
	}
	next := testkit.Config()
	o := next.Observers[0]
	o.ID = "new-private-observer"
	next.Observers = append(next.Observers, o)
	if e := config.Validate(next); e != nil {
		t.Fatal(e)
	}
	newViewer := strings.Repeat("d", 64)
	srv.Reload(next, Auth{Schema: 1, Tokens: []Principal{{secure.Hash(newViewer), "viewer", ""}}})
	_, e := srv.Ledger.Observe(model.Snapshot{Incidents: []model.Incident{{ID: "new-private-incident", Scope: "new-private-observer/node/home", Summary: "new-generation metadata", Since: now}}}, now, true)
	if e != nil {
		t.Fatal(e)
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot request did not finish")
	}
	if w.Code != 503 || !strings.Contains(w.Body.String(), "topology_reloaded") || w.Header().Get("Retry-After") != "1" || strings.Contains(w.Body.String(), "new-private") {
		t.Fatal("old authority received new metadata", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil); w.Code != 401 {
		t.Fatal("revoked token was accepted again", w.Code)
	}
	if w := request(h, "GET", "/v1/snapshot", newViewer, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "new-private-observer") || !strings.Contains(w.Body.String(), "new-private-incident") {
		t.Fatal("new viewer could not read current metadata", w.Code, w.Body.String())
	}
}
