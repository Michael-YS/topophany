// Package api exposes a versioned, authenticated collector API. Executors have
// observer-scoped ingestion rights; viewers cannot schedule arbitrary targets.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
)

type Principal struct {
	TokenSHA256 string `json:"token_sha256"`
	Role        string `json:"role"`
	Observer    string `json:"observer,omitempty"`
}
type Auth struct {
	Schema int         `json:"schema"`
	Tokens []Principal `json:"tokens"`
}

func LoadAuth(path string, c model.Config) (Auth, error) {
	var a Auth
	b, e := secure.PrivateRead(path)
	if e != nil {
		return a, e
	}
	if e = secure.Decode(strings.NewReader(string(b)), &a, 1<<20); e != nil {
		return a, e
	}
	return a, ValidateAuth(a, c)
}
func ValidateAuth(a Auth, c model.Config) error {
	if a.Schema != 1 || len(a.Tokens) == 0 || len(a.Tokens) > 256 {
		return errors.New("auth requires schema 1 and 1..256 tokens")
	}
	seen := map[string]bool{}
	obs := map[string]bool{}
	for _, v := range c.Observers {
		obs[v.ID] = true
	}
	for _, p := range a.Tokens {
		b, e := hex.DecodeString(p.TokenSHA256)
		if e != nil || len(b) != 32 || seen[strings.ToLower(p.TokenSHA256)] {
			return errors.New("invalid or duplicate token hash")
		}
		seen[strings.ToLower(p.TokenSHA256)] = true
		switch p.Role {
		case "viewer":
			if p.Observer != "" {
				return errors.New("viewer may not impersonate an observer")
			}
		case "worker":
			if !obs[p.Observer] {
				return errors.New("worker requires a known observer scope")
			}
		default:
			return errors.New("unknown role; collectors deliberately have no remote management role")
		}
	}
	return nil
}

type Server struct {
	Config             model.Config
	Instance           string
	Store              *store.Store
	Auth               Auth
	MaxSampleAge       time.Duration
	Now                func() time.Time
	mu                 sync.Mutex
	limits             map[string]limit
	slots              chan struct{}
	viewerSlots        chan struct{}
	inflight           map[string]int
	snapshotMu         sync.Mutex
	snapshotAt         time.Time
	snapshotRows       []model.Observation
	snapshotBeats      map[string]model.Heartbeat
	snapshotCache      map[string][]byte
	snapshotCacheBytes int
	snapshotMisses     map[string]int
	snapshotBuilds     int
	preauthSlots       chan struct{}
}
type admissionKey struct{}

type limit struct {
	At    time.Time
	Count int
}

func New(c model.Config, instance string, s *store.Store, a Auth) *Server {
	ids := make([]string, 0, len(c.Observers))
	for _, o := range c.Observers {
		ids = append(ids, o.ID)
	}
	if s != nil {
		s.SetObservers(ids)
	}
	return &Server{Config: c, Instance: instance, Store: s, Auth: a, MaxSampleAge: 24 * time.Hour, Now: time.Now, limits: map[string]limit{}, preauthSlots: make(chan struct{}, 16), slots: make(chan struct{}, 48), viewerSlots: make(chan struct{}, 16), inflight: map[string]int{}}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /v1/snapshot", s.authorize("viewer", s.snapshot))
	mux.HandleFunc("GET /v1/config", s.authorize("viewer", s.topology))
	mux.HandleFunc("GET /v1/history", s.authorize("viewer", s.history))
	mux.HandleFunc("POST /v1/observations", s.authorize("worker", s.ingest))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// Health, unknown routes, bad methods, authentication failures and
		// pre-auth rejections must not retain HTTP/1 listener slots. Only a
		// successfully authorized request opts back into connection reuse.
		if r.ProtoMajor == 1 {
			w.Header().Set("Connection", "close")
		}
		select {
		case s.preauthSlots <- struct{}{}:
		default:
			writeError(w, 429, "preauth_busy")
			return
		}
		var once sync.Once
		release := func() { once.Do(func() { <-s.preauthSlots }) }
		defer release()
		if !s.allowBudget("preauth", 300) {
			writeError(w, 429, "preauth_rate_limited")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), admissionKey{}, release))
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) authorize(role string, next func(http.ResponseWriter, *http.Request, Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Topophany"`)
			writeError(w, 401, "authentication_required")
			return
		}
		token := strings.TrimPrefix(raw, "Bearer ")
		if secure.CheckToken(token) != nil {
			writeError(w, 401, "invalid_token")
			return
		}
		hash := secure.Hash(token)
		var found Principal
		ok := false
		for _, p := range s.Auth.Tokens {
			if subtle.ConstantTimeCompare([]byte(strings.ToLower(p.TokenSHA256)), []byte(hash)) == 1 {
				found = p
				ok = true
			}
		}
		if !ok {
			writeError(w, 401, "invalid_token")
			return
		}
		if found.Role != role {
			writeError(w, 403, "role_forbidden")
			return
		}
		w.Header().Del("Connection")
		if release, ok := r.Context().Value(admissionKey{}).(func()); ok {
			release()
		}
		scope := hash
		if role == "worker" {
			scope = "worker:" + found.Observer
		}
		if !s.allow(scope) {
			w.Header().Set("Retry-After", "1")
			writeError(w, 429, "rate_limited")
			return
		}
		if !s.acquire(scope, role) {
			writeError(w, 429, "concurrency_limited")
			return
		}
		defer s.release(scope, role)
		next(w, r, found)
	}
}
func (s *Server) allow(key string) bool { return s.allowBudget(key, 30) }
func (s *Server) allowBudget(key string, maximum int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	v := s.limits[key]
	if now.Sub(v.At) >= time.Second {
		v = limit{At: now}
	}
	v.Count++
	s.limits[key] = v
	return v.Count <= maximum
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request, p Principal) {
	view := r.URL.Query().Get("observer")
	if view != "" {
		exists := false
		for _, o := range s.Config.Observers {
			exists = exists || o.ID == view
		}
		if !exists {
			writeError(w, 400, "unknown_observer")
			return
		}
	}
	s.writeSnapshot(w, view, p.TokenSHA256)
}
func (s *Server) topology(w http.ResponseWriter, r *http.Request, _ Principal) {
	writeJSON(w, 200, struct {
		Project  string       `json:"project"`
		Revision string       `json:"revision"`
		Topology model.Config `json:"topology"`
	}{s.Config.Project, config.PublicRevision(s.Config), config.Public(s.Config)})
}
func (s *Server) history(w http.ResponseWriter, r *http.Request, _ Principal) {
	id := r.URL.Query().Get("check")
	if _, ok := config.Check(s.Config, id); !ok {
		writeError(w, 404, "unknown_check")
		return
	}
	n := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		v, e := strconv.Atoi(q)
		if e != nil || v < 1 || v > 500 {
			writeError(w, 400, "invalid_limit")
			return
		}
		n = v
	}
	revision := config.PublicRevision(s.Config)
	entries := s.Store.History(id, config.Revision(s.Config), n)
	for i := range entries {
		entries[i].Revision = revision
	}
	writeJSON(w, 200, struct {
		Project  string              `json:"project"`
		Revision string              `json:"revision"`
		Entries  []model.Observation `json:"entries"`
	}{s.Config.Project, revision, entries})
}
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, p Principal) {
	if ct, _, e := mime.ParseMediaType(r.Header.Get("Content-Type")); e != nil || ct != "application/json" {
		writeError(w, 415, "json_required")
		return
	}
	defer r.Body.Close()
	// Bound body occupancy independently of the server's general read timeout.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(3 * time.Second))
	var b model.Batch
	if e := secure.Decode(r.Body, &b, 1<<20); e != nil {
		w.Header().Set("Connection", "close")
		writeError(w, 400, "invalid_batch_json")
		return
	}
	_ = controller.SetReadDeadline(time.Time{})
	if code, status := s.ValidateBatch(b, p.Observer); code != "" {
		writeError(w, status, code)
		return
	}
	if e := s.Store.Put(b); e != nil {
		switch {
		case errors.Is(e, store.ErrConflict):
			writeError(w, 409, "observation_id_conflict")
		case errors.Is(e, store.ErrScopeCapacity):
			writeError(w, 429, "observer_storage_quota")
		case errors.Is(e, store.ErrCapacity):
			writeError(w, 503, "storage_receipt_capacity")
		default:
			writeError(w, 503, "storage_unavailable")
		}
		return
	}
	writeJSON(w, 202, map[string]any{"project": s.Config.Project, "revision": config.Revision(s.Config), "accepted": len(b.Observations)})
}
func (s *Server) ValidateBatch(b model.Batch, observer string) (string, int) {
	if b.Project != s.Config.Project {
		return "project_mismatch", 409
	}
	revision := config.Revision(s.Config)
	if b.Revision != revision {
		return "revision_mismatch", 409
	}
	if len(b.Observations) > 100 || len(b.Observations) == 0 && b.Heartbeat == nil {
		return "invalid_batch_size", 400
	}
	now := s.Now()
	validTime := func(t time.Time) bool {
		return !t.IsZero() && !t.After(now.Add(30*time.Second)) && !t.Before(now.Add(-s.MaxSampleAge))
	}
	checks := make(map[string]model.Check, len(s.Config.Checks))
	for _, x := range s.Config.Checks {
		checks[x.ID] = x
	}
	for _, v := range b.Observations {
		x, ok := checks[v.Check]
		if !ok || x.Observer != observer || v.Observer != observer {
			return "observer_scope_violation", 403
		}
		if v.Project != b.Project || v.Revision != b.Revision {
			return "observation_contract_mismatch", 409
		}
		// Boot cannot contain the identity separator: otherwise observer=a,
		// boot=b.c can collide with observer=a.b, boot=c.
		if !config.ID(v.Boot) || strings.Contains(v.Boot, ".") || v.Sequence == 0 || v.ID != fmt.Sprintf("%s.%s.%d", observer, v.Boot, v.Sequence) {
			return "invalid_observation_identity", 400
		}
		if !validTime(v.SampledAt) {
			return "sample_time_out_of_range", 422
		}
		if !v.ReceivedAt.IsZero() {
			return "received_at_is_collector_owned", 400
		}
		switch v.Outcome {
		case "ok", "fail", "unknown", "error", "policy":
		default:
			return "invalid_outcome", 400
		}
		expected := "measured"
		switch x.Kind {
		case "mihomo", "tailscale", "wireguard":
			expected = "reported"
		}
		if v.Evidence != expected {
			return "evidence_kind_mismatch", 400
		}
		if v.DurationMS < 0 || v.DurationMS > int64(x.TimeoutSeconds+5)*1000 || len(v.Detail) > 1024 || len(v.Code) > 96 || len(v.Stage) > 96 || len(v.PathEvidence) > 128 || len(v.Facts) > 32 || len(v.Timings) > 16 {
			return "observation_budget_exceeded", 400
		}
		for k, val := range v.Facts {
			if len(k) > 128 || len(val) > 2048 || secure.Text(k) != k || secure.Text(val) != val {
				return "invalid_fact", 400
			}
		}
		for k, n := range v.Timings {
			if len(k) > 64 || secure.Text(k) != k || n < 0 || n > int64(x.TimeoutSeconds+5)*1000 {
				return "invalid_timing", 400
			}
		}
		if secure.Text(v.Detail) != v.Detail || secure.Text(v.Code) != v.Code || secure.Text(v.Stage) != v.Stage || secure.Text(v.PathEvidence) != v.PathEvidence {
			return "unsafe_diagnostic_text", 400
		}
	}
	if h := b.Heartbeat; h != nil {
		if h.Observer != observer {
			return "heartbeat_scope_violation", 403
		}
		if !config.ID(h.Boot) || !validTime(h.SampledAt) || !h.ReceivedAt.IsZero() {
			return "invalid_heartbeat", 400
		}
	}
	return "", 0
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// SaveAuth is used only by local initialization, never by a remote API.
func SaveAuth(path string, a Auth) error {
	b, e := json.MarshalIndent(a, "", "  ")
	if e != nil {
		return e
	}
	return secure.AtomicWrite(path, append(b, '\n'), os.FileMode(0600))
}
