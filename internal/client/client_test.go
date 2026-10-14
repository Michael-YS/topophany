package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const token = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func snapshot() model.Snapshot {
	c := testkit.Config()
	return model.Snapshot{Schema: model.SnapshotSchema, Project: c.Project, Revision: config.PublicRevision(c), Instance: "fixture", GeneratedAt: time.Now().UTC(), Topology: config.Public(c)}
}
func newClient(t *testing.T, urls ...string) *Client {
	t.Helper()
	p := Profile{Project: "lab"}
	for _, u := range urls {
		p.Servers = append(p.Servers, Endpoint{URL: u, Token: token})
	}
	c, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	return c
}
func jsonServer(t *testing.T, status int, v any) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); json.NewEncoder(w).Encode(v) }))
	t.Cleanup(s.Close)
	return s
}
func TestReadFailoverAndPinning(t *testing.T) {
	good := jsonServer(t, 200, snapshot())
	for _, status := range []int{500, 503, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			bad := jsonServer(t, status, map[string]string{"error": "down"})
			c := newClient(t, bad.URL, good.URL)
			s, url, e := c.Snapshot(context.Background(), "")
			if e != nil || url != good.URL || s.Project != "lab" {
				t.Fatal(s, url, e)
			}
		})
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c := newClient(t, dead.URL, good.URL)
	if _, url, e := c.Snapshot(context.Background(), ""); e != nil || url != good.URL {
		t.Fatal(url, e)
	}
}
func TestReadFailClosedSecurityBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		status int
		mutate func(*model.Snapshot)
	}{{"auth", 401, nil}, {"scope", 403, nil}, {"bad_request", 400, nil}, {"project", 200, func(s *model.Snapshot) { s.Project = "foreign" }}, {"embedded_project", 200, func(s *model.Snapshot) { s.Topology.Project = "foreign" }}, {"zero_generated", 200, func(s *model.Snapshot) { s.GeneratedAt = time.Time{} }}, {"view", 200, func(s *model.Snapshot) { s.ViewObserver = "cloud" }}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := snapshot()
			if tc.mutate != nil {
				tc.mutate(&s)
			}
			bad := jsonServer(t, tc.status, s)
			var hits atomic.Int32
			good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); json.NewEncoder(w).Encode(snapshot()) }))
			defer good.Close()
			c := newClient(t, bad.URL, good.URL)
			if _, _, e := c.Snapshot(context.Background(), ""); e == nil {
				t.Fatal("invalid response accepted")
			}
			if hits.Load() != 0 {
				t.Fatal("security failure silently fell through")
			}
		})
	}
}

// What a snapshot carries of the ledger is checked like the rest: records
// a ledger could have written, each incident open at most once, and in a
// view of one observer only the records of its scopes.
func TestLedgerRecordsAreChecked(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	open := model.IncidentRecord{ID: "0123456789ab/home/path/direct", Scope: "home/path/direct", Summary: "confirmed failure", Checks: []string{"home-http"}, State: "open", Since: now, Opened: now}
	closed := open
	closed.State, closed.Closed, closed.Ended, closed.Opened = "closed", now, "recovered", now.Add(-time.Hour)
	gone := closed
	gone.Checks, gone.Opened = []string{"removed-check"}, now.Add(-2*time.Hour)
	valid := snapshot()
	valid.Ledger = []model.IncidentRecord{open, closed, gone}
	srv := jsonServer(t, 200, valid)
	if s, _, e := newClient(t, srv.URL).Snapshot(context.Background(), ""); e != nil || len(s.Ledger) != 3 {
		t.Fatal("a valid ledger refused:", e)
	}
	for name, mutate := range map[string]func(*model.Snapshot){
		"state":        func(s *model.Snapshot) { s.Ledger[0].State = "lost" },
		"unended":      func(s *model.Snapshot) { s.Ledger[1].Ended = "" },
		"ending":       func(s *model.Snapshot) { s.Ledger[1].Ended = "forgotten" },
		"open_closed":  func(s *model.Snapshot) { s.Ledger[0].Closed = now },
		"twice_open":   func(s *model.Snapshot) { s.Ledger[1] = s.Ledger[0] },
		"repeated":     func(s *model.Snapshot) { s.Ledger[2] = s.Ledger[1] },
		"check":        func(s *model.Snapshot) { s.Ledger[0].Checks = []string{"bad id!"} },
		"unopened":     func(s *model.Snapshot) { s.Ledger[0].Opened = time.Time{} },
		"other_scope":  func(s *model.Snapshot) { s.ViewObserver = "cloud" },
		"over_budget":  func(s *model.Snapshot) { s.Ledger = append(s.Ledger, make([]model.IncidentRecord, 100)...) },
		"long_summary": func(s *model.Snapshot) { s.Ledger[0].Summary = strings.Repeat("x", 600) },
	} {
		t.Run(name, func(t *testing.T) {
			s := snapshot()
			s.Ledger = []model.IncidentRecord{open, closed, gone}
			mutate(&s)
			srv := jsonServer(t, 200, s)
			if _, _, e := newClient(t, srv.URL).Snapshot(context.Background(), ""); e == nil {
				t.Fatal("an invalid ledger accepted")
			}
		})
	}
}

// A collector of another release is passed over, as during a rolling
// upgrade, not taken for an attack; with none of this release the client
// says why. Fields a later schema 2 release adds are ignored.
func TestCollectorsOfAnotherReleaseArePassedOver(t *testing.T) {
	old := snapshot()
	old.Schema = 1
	older := jsonServer(t, 200, old)
	good := jsonServer(t, 200, snapshot())
	if _, url, e := newClient(t, older.URL, good.URL).Snapshot(context.Background(), ""); e != nil || url != good.URL {
		t.Fatal(url, e)
	}
	if _, _, e := newClient(t, older.URL).Snapshot(context.Background(), ""); !errors.Is(e, ErrNoCollector) || !strings.Contains(e.Error(), "schema 1") {
		t.Fatal("version skew not reported:", e)
	}
	later := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := json.Marshal(snapshot())
		w.Write(append([]byte(`{"added_later":{"x":1},`), raw[1:]...))
	}))
	defer later.Close()
	if _, _, e := newClient(t, later.URL).Snapshot(context.Background(), ""); e != nil {
		t.Fatal("a field added later broke the client:", e)
	}
}
func TestStaleAndFutureCollectorFailover(t *testing.T) {
	for _, delta := range []time.Duration{-time.Minute, time.Minute} {
		t.Run(delta.String(), func(t *testing.T) {
			s := snapshot()
			s.GeneratedAt = s.GeneratedAt.Add(delta)
			bad := jsonServer(t, 200, s)
			good := jsonServer(t, 200, snapshot())
			c := newClient(t, bad.URL, good.URL)
			if _, url, e := c.Snapshot(context.Background(), ""); e != nil || url != good.URL {
				t.Fatal(url, e)
			}
		})
	}
}
func TestSnapshotWireContractAndCancellation(t *testing.T) {
	var method, path, auth, query string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		query = r.URL.Query().Get("observer")
		v := snapshot()
		v.ViewObserver = query
		json.NewEncoder(w).Encode(v)
	}))
	defer s.Close()
	c := newClient(t, s.URL+"/prefix")
	if _, _, e := c.Snapshot(context.Background(), "home"); e != nil {
		t.Fatal(e)
	}
	if method != "GET" || path != "/prefix/v1/snapshot" || auth != "Bearer "+token || query != "home" {
		t.Fatal(method, path, auth, query)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := c.Snapshot(ctx, ""); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestMalformedSnapshotsAndBodyBudget(t *testing.T) {
	for _, body := range []string{"not json", strings.Repeat("x", 8<<20+1)} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		c := newClient(t, s.URL)
		if _, _, e := c.Snapshot(context.Background(), ""); e == nil {
			t.Fatal("bad payload accepted")
		}
		s.Close()
	}
}
func TestNoRedirectCredentialLeak(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer redirect.Close()
	c := newClient(t, redirect.URL)
	if _, _, e := c.Snapshot(context.Background(), ""); e == nil || hits.Load() != 0 {
		t.Fatal("redirect followed")
	}
}
func TestCollectorTLSValidation(t *testing.T) {
	cert, key, ca := testkit.Certificate(t)
	pair, e := tls.LoadX509KeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(snapshot()) }))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	s.StartTLS()
	defer s.Close()
	c := newClient(t, s.URL)
	if _, _, e := c.Snapshot(context.Background(), ""); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
	trusted, e := New(Profile{"lab", []Endpoint{{URL: s.URL, Token: token, CAFile: ca}}})
	if e != nil {
		t.Fatal(e)
	}
	defer trusted.Close()
	if _, _, e = trusted.Snapshot(context.Background(), ""); e != nil {
		t.Fatal(e)
	}
	if _, e = HTTP(Endpoint{URL: s.URL, CAFile: "missing"}); e == nil {
		t.Fatal("missing CA ignored")
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	os.WriteFile(bad, []byte("not a cert"), 0600)
	if _, e = HTTP(Endpoint{URL: s.URL, CAFile: bad}); e == nil {
		t.Fatal("bad CA ignored")
	}
}
func TestCredentialPersistenceAndValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config", "credentials.json")
	c := Credentials{Schema: 1, Active: "default", Profiles: map[string]Profile{"default": {"lab", []Endpoint{{URL: "http://127.0.0.1:7447", Token: token}}}}}
	if e := Save(p, c); e != nil {
		t.Fatal(e)
	}
	got, e := Load(p)
	if e != nil || got.Profiles["default"].Servers[0].Token != token {
		t.Fatal(e)
	}
	if _, e = DefaultPath(); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []Profile{{}, {"lab", nil}, {"lab", []Endpoint{{URL: "http://remote.example", Token: token}}}, {"lab", []Endpoint{{URL: "https://example.invalid", Token: token, TokenEnv: "X"}}}, {"lab", []Endpoint{{URL: "https://example.invalid", Token: "short"}}}, {"lab", []Endpoint{{URL: "https://example.invalid", Token: token}, {URL: "https://example.invalid", Token: token}}}} {
		if e := bad.Validate(); e == nil {
			t.Fatal("bad credentials accepted", bad)
		}
	}
	for _, b := range []string{`{"schema":2,"active":"default","profiles":{}}`, `{"schema":1,"active":"default","profiles":{"x":{"project":"lab","servers":[]}}}`, `{"schema":1,"unknown":true}`} {
		secure.AtomicWrite(p, []byte(b), 0600)
		if _, e = Load(p); e == nil {
			t.Fatal("invalid saved file accepted")
		}
	}
}
func TestTokenEnvAndNoEnvironmentProxy(t *testing.T) {
	s := jsonServer(t, 200, snapshot())
	t.Setenv("TOPH_VIEW_TOKEN", token)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	c, e := New(Profile{"lab", []Endpoint{{URL: s.URL, TokenEnv: "TOPH_VIEW_TOKEN"}}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, _, e = c.Snapshot(context.Background(), ""); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TOPH_VIEW_TOKEN", "")
	if _, _, e = c.Snapshot(context.Background(), ""); e == nil {
		t.Fatal("missing environment token silently accepted")
	}
}
func batch() model.Batch {
	c := testkit.Config()
	return model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, time.Now(), "ok")}}
}
func TestPostAcknowledgementMustMatchExactBatch(t *testing.T) {
	b := batch()
	cases := []struct {
		name      string
		status    int
		reply     any
		wantError bool
	}{{"accepted", 202, map[string]any{"project": b.Project, "revision": b.Revision, "accepted": 1}, false}, {"wrong_project", 202, map[string]any{"project": "foreign", "revision": b.Revision, "accepted": 1}, true}, {"wrong_revision", 202, map[string]any{"project": b.Project, "revision": "old", "accepted": 1}, true}, {"wrong_count", 202, map[string]any{"project": b.Project, "revision": b.Revision, "accepted": 0}, true}, {"not_created", 200, map[string]any{}, true}, {"auth", 401, map[string]string{"error": "invalid_token"}, true}, {"capacity", 503, map[string]string{"error": "full"}, true}, {"rate", 429, map[string]string{"error": "rate_limit"}, true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1/observations" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("wrong wire contract")
				}
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(tc.reply)
			}))
			defer s.Close()
			ep := Endpoint{URL: s.URL, Token: token}
			h, e := HTTP(ep)
			if e != nil {
				t.Fatal(e)
			}
			defer h.CloseIdleConnections()
			e = PostBatch(context.Background(), h, ep, b)
			if (e != nil) != tc.wantError {
				t.Fatal(e)
			}
			var he *HTTPError
			if tc.status == 401 {
				if !errors.As(e, &he) || !he.Permanent() {
					t.Fatal("auth is permanent")
				}
			}
			if tc.status == 503 || tc.status == 429 {
				if !errors.As(e, &he) || he.Permanent() {
					t.Fatal("retryable error quarantined")
				}
			}
		})
	}
	for _, status := range []int{408, 429, 500, 503} {
		if (&HTTPError{Status: status}).Permanent() {
			t.Fatal(status)
		}
	}
}

// History fails over as a snapshot read does, past collectors that are
// down or busy and never past a rejection, and refuses samples that are
// not the check's, the project's or the history's own revision.
func TestHistoryFailoverAndValidation(t *testing.T) {
	c := testkit.Config()
	rev := config.Revision(c)
	history := func() History {
		v := testkit.Observation(c, "home-http", 1, time.Now().UTC(), "fail")
		return History{Project: c.Project, Revision: rev, Check: "home-http", Contract: v.Contract, Entries: []model.Observation{v}}
	}
	good := jsonServer(t, 200, history())
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	busy := jsonServer(t, 503, map[string]string{"error": "down"})
	h, url, e := newClient(t, dead.URL, busy.URL, good.URL).History(context.Background(), "home-http", 5)
	if e != nil || url != good.URL || len(h.Entries) != 1 || h.Entries[0].Outcome != "fail" {
		t.Fatal(h, url, e)
	}
	for _, tc := range []struct {
		name   string
		status int
		mutate func(*History)
	}{{"auth", 401, nil}, {"unknown_check", 404, nil}, {"bad_request", 400, nil}, {"project", 200, func(h *History) { h.Project = "foreign" }}, {"check", 200, func(h *History) { h.Entries[0].Check = "home-ssh" }}, {"contract", 200, func(h *History) { h.Entries[0].Contract = strings.Repeat("0", 64) }}, {"revision", 200, func(h *History) { h.Entries[0].Revision = h.Revision }}, {"other_check", 200, func(h *History) { h.Check = "home-ssh" }}, {"too_many", 200, func(h *History) { h.Entries = append(h.Entries, h.Entries[0], h.Entries[0]) }}} {
		t.Run(tc.name, func(t *testing.T) {
			v := history()
			if tc.mutate != nil {
				tc.mutate(&v)
			}
			bad := jsonServer(t, tc.status, v)
			if _, _, e := newClient(t, bad.URL, good.URL).History(context.Background(), "home-http", 2); e == nil || errors.Is(e, ErrNoCollector) {
				t.Fatal("rejection fell through:", e)
			}
		})
	}
	if _, _, e := newClient(t, dead.URL, busy.URL).History(context.Background(), "home-http", 5); !errors.Is(e, ErrNoCollector) {
		t.Fatal("no collector answered, but:", e)
	}
	if _, _, e := newClient(t, dead.URL, busy.URL).Snapshot(context.Background(), ""); !errors.Is(e, ErrNoCollector) {
		t.Fatal("no collector answered a snapshot, but:", e)
	}
	if _, _, e := newClient(t, good.URL).History(context.Background(), "home-http", 501); e == nil {
		t.Fatal("limit out of range accepted")
	}
}
