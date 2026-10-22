package direct

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testObject = "aaaaaaaaaaaaaaa"
const testSubject = "uuuuuuuuuuuuuuu"

func jwt(exp time.Time, refreshable bool) string {
	b, _ := json.Marshal(map[string]any{"id": testSubject, "collectionId": "users", "type": "auth", "exp": exp.Unix(), "refreshable": refreshable})
	return "synthetic." + base64.RawURLEncoding.EncodeToString(b) + ".signature-canary"
}

type testHub struct {
	server      *httptest.Server
	mu          sync.Mutex
	counts      map[string]int
	status      int
	systems     int
	empty       bool
	at          time.Time
	token       string
	delay       time.Duration
	mfa         bool
	statsStatus int
}

func fixtureHub(t *testing.T) *testHub {
	t.Helper()
	h := &testHub{counts: map[string]int{}, systems: 1, at: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), token: jwt(time.Now().Add(time.Hour), true)}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.counts[r.Method+" "+r.URL.Path]++
		status, systems, empty, at, token, delay, mfa, statsStatus := h.status, h.systems, h.empty, h.at, h.token, h.delay, h.mfa, h.statsStatus
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if delay > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		if status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"message":"secret-canary-never-echo"}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "auth-with-password") || strings.HasSuffix(r.URL.Path, "auth-refresh"):
			if mfa {
				fmt.Fprint(w, `{"mfaId":"synthetic-mfa"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "record": map[string]string{"id": testSubject, "collectionName": "users"}})
		case strings.Contains(r.URL.Path, "users/records/"):
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": testSubject})
		case strings.Contains(r.URL.Path, "system_stats"):
			if statsStatus > 0 {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(statsStatus)
				return
			}
			items := []any{}
			if !empty {
				filter := r.URL.Query().Get("filter")
				id := testObject
				if len(filter) >= 25 {
					id = filter[10:25]
				}
				items = append(items, map[string]any{"id": "sssssssssssssss", "system": id, "type": "1m", "created": at.Format(time.RFC3339Nano), "stats": map[string]any{"cpu": 25, "mem": []uint64{9007199254740993, 4503599627370496, 0, 0}, "mp": 50, "d": 100, "du": 25, "dp": 25}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "perPage": 1, "totalItems": -1, "totalPages": -1, "items": items})
		case strings.Contains(r.URL.Path, "systems/records"):
			page := 1
			_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)
			items := []System{}
			for i := (page - 1) * 100; i < min(systems, page*100); i++ {
				id := fmt.Sprintf("%015d", i)
				if i == 0 {
					id = testObject
				}
				items = append(items, System{id, "Synthetic " + id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"page": page, "perPage": 100, "totalItems": systems, "totalPages": (systems + 99) / 100, "items": items})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}
func testRequest(h *testHub) Request {
	return Request{Schema: 1, RequestID: "test", Profile: "desktop", Source: SourceConfig{ID: "bs-example", Name: "Synthetic Hub", Adapter: "beszel", URL: h.server.URL, Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}, Auth: Auth{Mode: "password", Identity: "test@example.invalid", Password: "password-canary"}, Selected: []string{testObject}}
}
func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := NewStore(filepath.Join(t.TempDir(), "direct"))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func saveFixture(t *testing.T, s *Store, r Request) Profile {
	t.Helper()
	c, h, e := s.Authenticate(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	systems, _, e := h.Discover(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	p, _, e := s.Save(context.Background(), r, c, systems)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestProfileAtomicIdentityAndAuth(t *testing.T) {
	h := fixtureHub(t)
	s := testStore(t)
	r := testRequest(h)
	t.Setenv("TOPO_DIRECT_TEST_TOKEN", h.token)
	for _, mode := range []string{"password", "token", "env", "saved"} {
		t.Run(mode, func(t *testing.T) {
			r.Auth = Auth{Mode: mode}
			switch mode {
			case "password":
				r.Auth.Identity = "synthetic@example.invalid"
				r.Auth.Password = "password-canary"
			case "token":
				r.Auth.Token = h.token
			case "env":
				r.Auth.Name = "TOPO_DIRECT_TEST_TOKEN"
			}
			p := saveFixture(t, s, r)
			if len(p.Sources) != 1 || p.Sources[0].Nodes[0].ID != "bs-example."+testObject {
				t.Fatal(p)
			}
			c, e := p.Config()
			if e != nil {
				t.Fatal(e)
			}
			if c.Sources[0].MaxPages != 1 || c.Observers[0].Node != "" || c.Checks[0].External.PrimaryDisk != "root" || c.Checks[0].External.Assertions != nil {
				t.Fatal(c)
			}
			if r.BaseRevision != "" {
				old, _ := s.Load(r.Profile)
				if old.Dataset != p.Dataset {
					t.Fatal("rename/token changed dataset")
				}
			}
			b, e := os.ReadFile(filepath.Join(s.Root, "profiles.json"))
			if e != nil {
				t.Fatal(e)
			}
			for _, secret := range []string{"password-canary", h.token, "TOPO_DIRECT_TEST_TOKEN"} {
				if strings.Contains(string(b), secret) {
					t.Fatal("public secret leak")
				}
			}
			r.BaseRevision = p.Revision
		})
	}
	// Return to password because env values deliberately were not copied.
	r.Auth = Auth{Mode: "password", Identity: "synthetic@example.invalid", Password: "password-canary"}
	before, _ := s.Load(r.Profile)
	r.Source.Name = "Renamed"
	p := saveFixture(t, s, r)
	if before.Dataset != p.Dataset || before.Revision == p.Revision {
		t.Fatal("revision/dataset coupling")
	}
	r.BaseRevision = p.Revision
	c, hub, e := s.Authenticate(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	hub.Close()
	s.Write = func(path string, b []byte, m os.FileMode) error {
		if filepath.Base(path) == "profiles.json" {
			return errors.New("injected")
		}
		return os.WriteFile(path, b, m)
	}
	if _, _, e = s.Save(context.Background(), r, c, []System{{testObject, "Synthetic"}}); AsError(e).Code != "storage_error" {
		t.Fatal(e)
	}
	after, e := s.Load(r.Profile)
	if e != nil || after.Revision != p.Revision {
		t.Fatal("failed commit damaged old config", e)
	}
	s.Write = func(string, []byte, os.FileMode) error { return errors.New("injected") }
	if _, _, e = s.Save(context.Background(), r, c, []System{{testObject, "Synthetic"}}); AsError(e).Code != "credential_storage_error" {
		t.Fatal(e)
	}
}
func TestDiscoveryAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		mfa    bool
		code   string
	}{{"401", 401, false, "auth_required"}, {"403", 403, false, "forbidden"}, {"429", 429, false, "rate_limited"}, {"MFA", 0, true, "mfa_required"}} {
		t.Run(tc.name, func(t *testing.T) {
			h := fixtureHub(t)
			h.status = tc.status
			h.mfa = tc.mfa
			s := testStore(t)
			_, _, e := s.Authenticate(context.Background(), testRequest(h))
			if AsError(e).Code != tc.code {
				t.Fatal(e)
			}
			if strings.Contains(e.Error(), "secret-canary") {
				t.Fatal("leaked error")
			}
		})
	}
	h := fixtureHub(t)
	s := testStore(t)
	r := testRequest(h)
	h.systems = 1001
	c, hub, e := s.Authenticate(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer hub.Close()
	systems, truncated, e := hub.Discover(context.Background(), c)
	if e != nil || !truncated || len(systems) != 1000 {
		t.Fatal(len(systems), truncated, e)
	}
	h.mu.Lock()
	h.systems = 0
	h.mu.Unlock()
	systems, truncated, e = hub.Discover(context.Background(), c)
	if e != nil || truncated || len(systems) != 0 {
		t.Fatal(e)
	}
	h.mu.Lock()
	h.systems = 1
	h.mu.Unlock()
	r.Auth = Auth{Mode: "token", Token: jwt(time.Now().Add(time.Hour), false)}
	if _, hub, e := s.Authenticate(context.Background(), r); e != nil {
		t.Fatal(e)
	} else {
		hub.Close()
	}
	h.server.Close()
	if _, _, e = s.Authenticate(context.Background(), r); AsError(e).Code != "connection_failed" {
		t.Fatal(e)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tls.Close()
	r.Source.URL = tls.URL
	if _, _, e = s.Authenticate(context.Background(), r); AsError(e).Code != "tls_error" {
		t.Fatal(e)
	}
}
func TestSelectionConflictRemoveStorage(t *testing.T) {
	h := fixtureHub(t)
	s := testStore(t)
	r := testRequest(h)
	p := saveFixture(t, s, r)
	c, hub, e := s.Authenticate(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	hub.Close()
	if _, _, e = s.Save(context.Background(), r, c, []System{{testObject, "Synthetic"}}); AsError(e).Code != "profile_conflict" {
		t.Fatal(e)
	}
	r.BaseRevision = p.Revision
	r.Selected = []string{"invalid"}
	if _, _, e = s.Save(context.Background(), r, c, []System{{testObject, "Synthetic"}}); AsError(e).Code != "selection_invalid" {
		t.Fatal(e)
	}
	r.SourceID = "missing"
	if _, e = s.Remove(context.Background(), r); AsError(e).Code != "source_not_found" {
		t.Fatal(e)
	}
	r.SourceID = r.Source.ID
	empty, e := s.Remove(context.Background(), r)
	if e != nil || len(empty.Sources) != 0 || empty.Dataset == p.Dataset {
		t.Fatal(e)
	}
	loaded, e := s.Load(r.Profile)
	if e != nil || len(loaded.Sources) != 0 {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.Root, "profiles.json"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); AsError(e).Code != "storage_error" {
		t.Fatal(e)
	}
	if e = os.Chmod(s.Root, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); AsError(e).Code != "storage_error" {
		t.Fatal(e)
	}
}
func TestInputStrictAndBounded(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"schema":1,"profile":"desktop","request_id":null}`, `{"schema":1,"schema":1,"profile":"desktop"}`, `{"schema":1,"profile":"desktop","source":{"interval_seconds":0},"auth":{"mode":"saved","password":"x"}}`, strings.Repeat(" ", MaxInput+1)} {
		if _, e := Decode(strings.NewReader(raw)); e == nil {
			t.Fatalf("accepted %.100s", raw)
		}
	}
	r, e := Decode(strings.NewReader(`{"schema":1,"profile":"desktop","future":true,"source":{"id":"bs-a","name":"A","adapter":"beszel","url":"http://127.0.0.1:1","credential_env":"ATTACKER"},"auth":{"mode":"token","token":"synthetic"}}`))
	if e != nil || r.Source.Interval != 60 || r.Source.Timeout != 8 {
		t.Fatal(r, e)
	}
}
func TestCredentialProcess(t *testing.T) {
	if os.Getenv("TOPO_DIRECT_CHILD") != "1" {
		return
	}
	s, e := NewStore(os.Getenv("TOPO_DIRECT_TEST_ROOT"))
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Load("desktop")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e = s.Token(ctx, p.Profile, p.Sources[0], time.Now()); e != nil {
		t.Fatal(e)
	}
}
func TestRefreshTwoProcessesAndStorageFailure(t *testing.T) {
	h := fixtureHub(t)
	s := testStore(t)
	r := testRequest(h)
	p := saveFixture(t, s, r)
	entry := p.Sources[0]
	cred, e := s.credential(entry.Credential)
	if e != nil {
		t.Fatal(e)
	}
	cred.Token = jwt(time.Now().Add(30*time.Second), true)
	b, _ := json.Marshal(cred)
	path := filepath.Join(s.Root, "credential-"+entry.Credential+".json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	before := h.counts["POST /api/collections/users/auth-refresh"]
	h.mu.Unlock()
	commands := []*exec.Cmd{}
	for range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCredentialProcess$")
		cmd.Env = append(os.Environ(), "TOPO_DIRECT_CHILD=1", "TOPO_DIRECT_TEST_ROOT="+s.Root)
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if e = cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	h.mu.Lock()
	got := h.counts["POST /api/collections/users/auth-refresh"] - before
	h.mu.Unlock()
	if got != 1 {
		t.Fatalf("refresh requests %d", got)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	s.Write = func(string, []byte, os.FileMode) error { return errors.New("injected") }
	if _, e = s.Token(context.Background(), p.Profile, entry, time.Now()); AsError(e).Code != "credential_storage_error" {
		t.Fatal(e)
	}
	old, _ := s.credential(entry.Credential)
	if old.Version != cred.Version {
		t.Fatal("write failure changed credential")
	}
}
func TestRefreshAndDeleteSerialized(t *testing.T) {
	h := fixtureHub(t)
	s := testStore(t)
	r := testRequest(h)
	p := saveFixture(t, s, r)
	entry := p.Sources[0]
	cred, _ := s.credential(entry.Credential)
	cred.Token = jwt(time.Now().Add(30*time.Second), true)
	b, _ := json.Marshal(cred)
	_ = os.WriteFile(filepath.Join(s.Root, "credential-"+entry.Credential+".json"), b, 0600)
	var refreshing atomic.Bool
	orig := s.Write
	s.Write = func(path string, b []byte, m os.FileMode) error {
		if strings.Contains(path, "credential-") {
			refreshing.Store(true)
			time.Sleep(50 * time.Millisecond)
		}
		return orig(path, b, m)
	}
	done := make(chan error, 1)
	go func() { _, e := s.Token(context.Background(), p.Profile, entry, time.Now()); done <- e }()
	deadline := time.Now().Add(time.Second)
	for !refreshing.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	r.BaseRevision = p.Revision
	r.SourceID = r.Source.ID
	if _, e := s.Remove(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	loaded, e := s.Load(p.Profile)
	if e != nil || len(loaded.Sources) != 0 {
		t.Fatal("late refresh resurrected source", e)
	}
}

func TestGeneratedProfileBudgets(t *testing.T) {
	h := fixtureHub(t)
	r := testRequest(h)
	p := Profile{Profile: "budget", Project: "direct-budget", Sources: []Entry{}}
	total := 0
	for sourceIndex := 0; sourceIndex < 13; sourceIndex++ {
		s := r.Source
		s.ID = fmt.Sprintf("bs-%d", sourceIndex)
		entry := Entry{Source: s}
		count := 20
		if sourceIndex == 12 {
			count = 16
		}
		for i := 0; i < count; i++ {
			obj := fmt.Sprintf("%015d", total)
			total++
			entry.Selected = append(entry.Selected, obj)
			entry.Nodes = append(entry.Nodes, Node{s.ID + "." + obj, obj, "Synthetic"})
		}
		p.Sources = append(p.Sources, entry)
	}
	c, e := p.Config()
	if e != nil || len(c.Checks) != 256 || c.Sources[0].MaxPages != 20 {
		t.Fatal(len(c.Checks), e)
	}
	p.Sources[12].Selected = append(p.Sources[12].Selected, testObject)
	p.Sources[12].Nodes = append(p.Sources[12].Nodes, Node{p.Sources[12].Source.ID + "." + testObject, testObject, "Synthetic"})
	if _, e = p.Config(); AsError(e).Code != "selection_limit" {
		t.Fatal(e)
	}
	p.Sources = p.Sources[:1]
	p.Sources[0].Selected = append(p.Sources[0].Selected, testObject)
	p.Sources[0].Nodes = append(p.Sources[0].Nodes, Node{p.Sources[0].Source.ID + "." + testObject, testObject, "Synthetic"})
	if _, e = p.Config(); e == nil {
		t.Fatal("accepted 21 objects")
	}
}

func TestAuthRetryAfterPersistsAcrossProcesses(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	entry := p.Sources[0]
	c, _ := st.credential(entry.Credential)
	c.Token = jwt(time.Now().Add(30*time.Second), true)
	b, _ := json.Marshal(c)
	_ = os.WriteFile(filepath.Join(st.Root, "credential-"+entry.Credential+".json"), b, 0600)
	h.mu.Lock()
	h.status = 429
	h.mu.Unlock()
	_, e := st.Token(context.Background(), p.Profile, entry, time.Now())
	if AsError(e).Code != "rate_limited" || AsError(e).RetryAt.Before(time.Now().Add(59*time.Second)) {
		t.Fatal(e)
	}
	another, _ := NewStore(st.Root)
	if _, e = another.Token(context.Background(), p.Profile, entry, time.Now()); AsError(e).Code != "rate_limited" {
		t.Fatal(e)
	}
	h.mu.Lock()
	calls := h.counts["POST /api/collections/users/auth-refresh"]
	h.mu.Unlock()
	if calls != 1 {
		t.Fatal("auth Retry-After was bypassed", calls)
	}
	after, _ := st.Load(p.Profile)
	if after.Revision != p.Revision || after.Dataset != p.Dataset {
		t.Fatal("auth cooldown changed public identity")
	}
}

func TestRemovalWinsAgainstLateNetworkRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	oldToken := jwt(time.Now().Add(30*time.Second), true)
	freshToken := jwt(time.Now().Add(time.Hour), true)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": freshToken, "record": map[string]string{"id": testSubject, "collectionName": "users"}})
	}))
	defer hub.Close()
	st := testStore(t)
	r := Request{Profile: "desktop", Source: SourceConfig{ID: "bs-example", Name: "Synthetic", Adapter: "beszel", URL: hub.URL, Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}, Selected: []string{testObject}}
	p, _, e := st.Save(context.Background(), r, Credential{Mode: "token", Token: oldToken, Renewable: true, Subject: "users:" + testSubject}, []System{{testObject, "Synthetic"}})
	if e != nil {
		t.Fatal(e)
	}
	entry := p.Sources[0]
	before, _ := st.credential(entry.Credential)
	done := make(chan error, 1)
	go func() { _, e := st.Token(context.Background(), p.Profile, entry, time.Now()); done <- e }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	r.BaseRevision = p.Revision
	r.SourceID = r.Source.ID
	if _, e = st.Remove(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; AsError(e).Code != "source_not_found" {
		t.Fatal(e)
	}
	after, _ := st.credential(entry.Credential)
	if after.Version != before.Version || after.Token != oldToken {
		t.Fatal("late refresh overwrote removed credential")
	}
}
func TestPrivateCredentialPermissionsAndInvisibleSelection(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	r := testRequest(h)
	p := saveFixture(t, st, r)
	r.BaseRevision = p.Revision
	r.Selected = []string{"bbbbbbbbbbbbbbb"}
	if _, _, e := st.Save(context.Background(), r, Credential{Mode: "token", Token: h.token, Subject: "users:" + testSubject}, []System{{testObject, "Synthetic"}}); AsError(e).Code != "selection_invalid" {
		t.Fatal(e)
	}
	path := filepath.Join(st.Root, "credential-"+p.Sources[0].Credential+".json")
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := st.Token(context.Background(), p.Profile, p.Sources[0], time.Now()); AsError(e).Code != "credential_storage_error" {
		t.Fatal(e)
	}
}
