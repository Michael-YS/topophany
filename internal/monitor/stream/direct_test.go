package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestDirectManagerOwnsProjectionAndRejectsConflicts(t *testing.T) {
	now := time.Now().UTC()
	var count atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[{"id":"sssssssssssssss","system":"aaaaaaaaaaaaaaa","type":"1m","created":%q,"stats":{"cpu":25,"m":8,"mu":4,"mp":50,"d":100,"du":25,"dp":25}}]}`, now.Format(time.RFC3339Nano))
	}))
	defer hub.Close()
	root := filepath.Join(t.TempDir(), "direct")
	store, e := direct.NewStore(root)
	if e != nil {
		t.Fatal(e)
	}
	req := direct.Request{Profile: "desktop", Source: direct.SourceConfig{ID: "bs-example", Name: "Synthetic", Adapter: "beszel", URL: hub.URL, Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}, Selected: []string{"aaaaaaaaaaaaaaa"}}
	p, _, e := store.Save(context.Background(), req, direct.Credential{Mode: "token", Token: "synthetic-canary-token", Subject: "users:uuuuuuuuuuuuuuu"}, []direct.System{{ID: "aaaaaaaaaaaaaaa", Name: "Synthetic server"}})
	if e != nil {
		t.Fatal(e)
	}
	template := monitor.Template{Schema: 1, Title: "D1", Rows: []monitor.Row{{ID: "status", Metric: "sources.status", Visual: "status"}}}
	template.Bar.Row = "status"
	request := Request{Schema: 1, Source: "direct", Profile: p.Profile, ProfileRevision: p.Revision, Interval: "1s", Template: template}
	if _, e := Decode(line(request)); e != nil {
		t.Fatal(e)
	}
	m := NewDirectManager(root, time.Now)
	defer m.Close()
	old, err := m.Open(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := (view.View{Snapshot: old.Initial.Snapshot, Now: now}).SourcesStatus()
	if status.State != "loading" {
		t.Fatal(status)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		in, e := old.Fetch(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		status, _ = (view.View{Snapshot: in.Snapshot, Now: time.Now()}).SourcesStatus()
		if status.State == "ready" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if status.State != "ready" {
		t.Fatal(status)
	}
	current := m.current
	request.RequestID = "replacement"
	request.Template.Title = "Changed"
	next, err := m.Open(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	if m.current != current {
		t.Fatal("projection replaced collection")
	}
	if _, e := next.Fetch(context.Background()); e != nil {
		t.Fatal(e)
	}
	request.ProfileRevision = "obsolete"
	if _, err = m.Open(context.Background(), request); err == nil || err.Code != "profile_conflict" {
		t.Fatal(err)
	}
	if m.current != current || count.Load() != 1 {
		t.Fatal("conflict/reprojection collected")
	}
	next.Close()
	if m.current != nil {
		t.Fatal("session leaked after final owner")
	}
}
func TestD1RequestFieldsAndLimits(t *testing.T) {
	var r Request
	if e := json.Unmarshal([]byte(`{"schema":1,"source":"direct","profile":"desktop","template":{"schema":1,"title":"D1","bar":{"row":"status"},"rows":[{"id":"status","metric":"sources.status","visual":"status"}]}}`), &r); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Request){func(r *Request) { r.Profile = "" }, func(r *Request) { r.Credentials = "/synthetic" }, func(r *Request) { r.Lang = "en" }, func(r *Request) { r.Scenario = 1 }, func(r *Request) { r.ProfileRevision = string(make([]byte, 129)) }, func(r *Request) { r.RetryNonce = string(make([]byte, 129)) }} {
		candidate := r
		change(&candidate)
		if _, e := Decode(line(candidate)); e == nil {
			t.Fatal("accepted invalid D1 request")
		}
	}
	for i := 1; i < 129; i++ {
		r.Template.Rows = append(r.Template.Rows, monitor.Row{ID: fmt.Sprintf("row-%d", i), Metric: "sources.status", Visual: "status"})
	}
	if _, e := Decode(line(r)); e == nil {
		t.Fatal("accepted 129 rows")
	}
}
