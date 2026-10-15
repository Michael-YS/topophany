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
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/rollup"
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

// Server is a collector's API. Config and Auth may be set before it serves;
// afterwards only Reload changes them.
type Server struct {
	Config             model.Config
	Instance           string
	Store              *store.Store
	Ledger             *ledger.Ledger // what snapshots carry of past incidents; nil for none
	Auth               Auth
	configMu           sync.RWMutex
	gen                *generation  // what Config and Auth give every request; nil until first asked
	reloadMu           sync.RWMutex // held by Reload, and by ingest from its check of the generation until it stores
	MaxSampleAge       time.Duration
	Now                func() time.Time
	mu                 sync.Mutex
	limits             map[string]limit
	slots              chan struct{}
	viewerSlots        chan struct{}
	inflight           map[string]int
	snapshotMu         sync.Mutex
	snapshotAt         time.Time
	snapshotGen        *generation // what the cached snapshots were built under
	snapshotRows       []model.Observation
	snapshotCarries    []engine.Carry
	snapshotBeats      map[string]model.Heartbeat
	snapshotCache      map[string][]byte
	snapshotAvailable  map[string][]model.Availability // by check, as of snapshotAt
	snapshotCacheBytes int
	snapshotMisses     map[string]int
	snapshotBuilds     int
	preauthSlots       chan struct{}
}
type authorityKey struct{}

// An authority is what a request was admitted with: the principal its
// token names and the generation that token was found in. The request is
// served under that generation throughout, so one admitted before a
// reload never acts under the topology loaded after it.
type authority struct {
	Principal
	gen *generation
}

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

// A generation is what every request served under one topology and its
// tokens shares, worked out once when they are loaded rather than by each
// request: its revision, its checks and their contracts, and what viewers
// may see of them. Nothing in it is changed after it is made.
type generation struct {
	config          model.Config
	auth            Auth
	revision        string
	checks          map[string]model.Check
	contracts       map[string]string // by check
	publicRevision  string
	publicContracts map[string]string // by check
	topology        []byte            // the body GET /v1/config answers with
}

func newGeneration(c model.Config, a Auth) *generation {
	public := config.Public(c)
	g := &generation{config: c, auth: a, revision: config.Revision(c), checks: make(map[string]model.Check, len(c.Checks)), contracts: config.Contracts(c), publicRevision: config.Revision(public), publicContracts: config.Contracts(public)}
	for _, x := range c.Checks {
		g.checks[x.ID] = x
	}
	g.topology, _ = json.Marshal(struct {
		Project  string       `json:"project"`
		Revision string       `json:"revision"`
		Topology model.Config `json:"topology"`
	}{c.Project, g.publicRevision, public})
	g.topology = append(g.topology, '\n')
	return g
}

// generation is the generation of the topology and tokens a request
// admitted now is served under.
func (s *Server) generation() *generation {
	s.configMu.RLock()
	g := s.gen
	s.configMu.RUnlock()
	if g != nil {
		return g
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.gen == nil {
		s.gen = newGeneration(s.Config, s.Auth)
	}
	return s.gen
}

// Reload makes c and a the collector's topology and tokens, as on SIGHUP.
// Requests already past authorization read under the old ones; a batch
// admitted under them is refused for its worker to send again, unless it
// was stored before the reload. The store counts samples under c from now
// on, and no snapshot built before is served again.
func (s *Server) Reload(c model.Config, a Auth) {
	g := newGeneration(c, a)
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.configMu.Lock()
	s.Config, s.Auth, s.gen = c, a, g
	s.configMu.Unlock()
	if s.Store != nil {
		ids := make([]string, 0, len(c.Observers))
		for _, o := range c.Observers {
			ids = append(ids, o.ID)
		}
		s.Store.SetObservers(ids)
		s.Store.SetCurrent(g.revision, g.contracts, config.Stale(c))
	}
	s.snapshotMu.Lock()
	s.snapshotCache = nil
	s.snapshotMu.Unlock()
}

// Observe judges the evidence of all observers at now, as a snapshot of
// them would, and records in the ledger the incidents that open, recover
// and close (see ledger.Observe). It returns that snapshot too.
func (s *Server) Observe(now time.Time, settled bool) (model.Snapshot, []ledger.Change, error) {
	c := s.generation().config
	rows, carries, beats := s.Store.StateView()
	snap := engine.BuildCarried(c, s.Instance, "", rows, carries, beats, now)
	if s.Ledger == nil {
		return snap, nil, nil
	}
	changes, e := s.Ledger.Observe(snap, now, settled)
	return snap, changes, e
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /v1/snapshot", s.authorize("viewer", s.snapshot))
	mux.HandleFunc("GET /v1/config", s.authorize("viewer", s.topology))
	mux.HandleFunc("GET /v1/history", s.authorize("viewer", s.history))
	mux.HandleFunc("GET /v1/rollups", s.authorize("viewer", s.rollups))
	mux.HandleFunc("POST /v1/observations", s.authorize("worker", s.ingest))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// Health, unknown routes, bad methods, authentication failures and
		// pre-auth rejections must not retain listener slots. Only a
		// successfully authorized request opts back into connection reuse.
		// net/http translates this into GOAWAY for HTTP/2 and removes the
		// header from the wire, closing the connection once streams finish.
		w.Header().Set("Connection", "close")
		// A request that authenticates answers to the budgets of its own
		// principal, whatever it asks for, never to the slots and the rate
		// shared by everything anonymous, so anonymous traffic, however much
		// of it, cannot refuse a worker or a viewer.
		if a, ok := s.admit(r); ok {
			if !s.allow(scope(a.Principal)) {
				w.Header().Set("Retry-After", "1")
				writeError(w, 429, "rate_limited")
				return
			}
			mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authorityKey{}, a)))
			return
		}
		select {
		case s.preauthSlots <- struct{}{}:
		default:
			writeError(w, 429, "preauth_busy")
			return
		}
		defer func() { <-s.preauthSlots }()
		if !s.allowBudget("preauth", 300) {
			writeError(w, 429, "preauth_rate_limited")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// admit is the authority of the token r carries, if any, found in the
// current generation: one SHA-256 and a constant-time comparison with each
// token, cheap enough to do before any budget is charged.
func (s *Server) admit(r *http.Request) (authority, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || secure.CheckToken(token) != nil {
		return authority{}, false
	}
	hash := secure.Hash(token)
	found := authority{gen: s.generation()}
	ok = false
	for _, p := range found.gen.auth.Tokens {
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(p.TokenSHA256)), []byte(hash)) == 1 {
			found.Principal = p
			ok = true
		}
	}
	return found, ok
}
func (s *Server) authorize(role string, next func(http.ResponseWriter, *http.Request, authority)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, ok := r.Context().Value(authorityKey{}).(authority)
		if !ok {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				w.Header().Set("WWW-Authenticate", `Bearer realm="Topophany"`)
				writeError(w, 401, "authentication_required")
				return
			}
			writeError(w, 401, "invalid_token")
			return
		}
		if found.Role != role {
			writeError(w, 403, "role_forbidden")
			return
		}
		w.Header().Del("Connection")
		if !s.acquire(scope(found.Principal), role) {
			writeError(w, 429, "concurrency_limited")
			return
		}
		defer s.release(scope(found.Principal), role)
		next(w, r, found)
	}
}

// scope is what a principal's rate and concurrency are counted under: a
// worker's observer, whatever token it uses, or a viewer's token.
func scope(p Principal) string {
	if p.Role == "worker" {
		return "worker:" + p.Observer
	}
	return strings.ToLower(p.TokenSHA256)
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
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request, a authority) {
	view := r.URL.Query().Get("observer")
	if view != "" {
		exists := false
		for _, o := range a.gen.config.Observers {
			exists = exists || o.ID == view
		}
		if !exists {
			writeError(w, 400, "unknown_observer")
			return
		}
	}
	s.writeSnapshot(w, a.gen, view, a.TokenSHA256)
}
func (s *Server) topology(w http.ResponseWriter, r *http.Request, a authority) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.gen.topology)
}
func (s *Server) history(w http.ResponseWriter, r *http.Request, a authority) {
	g := a.gen
	id := r.URL.Query().Get("check")
	if _, ok := g.checks[id]; !ok {
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
	contract := g.publicContracts[id]
	entries := s.Store.History(id, map[string]string{id: g.contracts[id]}, g.revision, n)
	for i := range entries {
		entries[i].Contract, entries[i].Revision = contract, ""
	}
	writeJSON(w, 200, struct {
		Project  string              `json:"project"`
		Revision string              `json:"revision"`
		Check    string              `json:"check"`
		Contract string              `json:"contract"`
		Entries  []model.Observation `json:"entries"`
	}{g.config.Project, g.publicRevision, id, contract, entries})
}

// rollups lays a check's rollups under its current contract out in slots
// of step (5m, 1h or 1d) over the span before now, with their total.
func (s *Server) rollups(w http.ResponseWriter, r *http.Request, a authority) {
	g := a.gen
	q := r.URL.Query()
	id := q.Get("check")
	if _, ok := g.checks[id]; !ok {
		writeError(w, 404, "unknown_check")
		return
	}
	name := q.Get("step")
	if name == "" {
		name = "1h"
	}
	step, ok := rollup.Steps[name]
	if !ok {
		writeError(w, 400, "invalid_step")
		return
	}
	span := rollup.DefaultSpan(step)
	if v := q.Get("span"); v != "" {
		var e error
		if span, e = rollup.ParseSpan(v); e != nil {
			writeError(w, 400, "invalid_span")
			return
		}
	}
	if rollup.Layout(step, span) != nil {
		writeError(w, 400, "invalid_span")
		return
	}
	now := s.Now().UTC()
	total, slots, since := s.Store.Rollups(id, g.contracts[id], now.Add(-span).Truncate(step), step, now)
	writeJSON(w, 200, struct {
		Project     string         `json:"project"`
		Revision    string         `json:"revision"`
		Check       string         `json:"check"`
		Contract    string         `json:"contract"`
		GeneratedAt time.Time      `json:"generated_at"`
		Step        int64          `json:"step_seconds"`
		Since       time.Time      `json:"since,omitzero"`
		Total       model.Rollup   `json:"total"`
		Slots       []model.Rollup `json:"slots"`
	}{g.config.Project, g.publicRevision, id, g.publicContracts[id], now, int64(step / time.Second), since, total, slots})
}
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, a authority) {
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
	if status, code := s.put(a, b); code != "" {
		if code == "topology_reloaded" {
			w.Header().Set("Retry-After", "1")
		}
		writeError(w, status, code)
		return
	}
	// The acknowledgement echoes the batch's revision: under schema 2 it
	// describes the sender, not what the collector counts.
	writeJSON(w, 202, map[string]any{"project": b.Project, "revision": b.Revision, "accepted": len(b.Observations)})
}

// put judges b under the generation a was admitted with and stores it,
// or says why not. That generation must still be current when b is
// stored: a reload while the body was read may have revoked the token
// that admitted it, so the worker sends it again and is admitted, or not,
// afresh.
func (s *Server) put(a authority, b model.Batch) (int, string) {
	s.reloadMu.RLock()
	defer s.reloadMu.RUnlock()
	if s.generation() != a.gen {
		return 503, "topology_reloaded"
	}
	if code, status := a.gen.validateBatch(b, a.Observer, s.Now(), s.MaxSampleAge); code != "" {
		return status, code
	}
	if e := s.Store.Put(b); e != nil {
		switch {
		case errors.Is(e, store.ErrConflict):
			return 409, "observation_id_conflict"
		case errors.Is(e, store.ErrScopeCapacity):
			return 429, "observer_storage_quota"
		case errors.Is(e, store.ErrCapacity):
			return 503, "storage_receipt_capacity"
		default:
			return 503, "storage_unavailable"
		}
	}
	return 0, ""
}

// ValidateBatch judges b from observer under the current topology.
func (s *Server) ValidateBatch(b model.Batch, observer string) (string, int) {
	return s.generation().validateBatch(b, observer, s.Now(), s.MaxSampleAge)
}
func (g *generation) validateBatch(b model.Batch, observer string, now time.Time, maxAge time.Duration) (string, int) {
	if b.Project != g.config.Project {
		return "project_mismatch", 409
	}
	// A schema 1 batch counts only under the very topology it was taken
	// with; a schema 2 observation, under its check's contract.
	revision := g.revision
	legacy := b.Schema == 0 || b.Schema == 1
	switch {
	case !legacy && b.Schema != model.BatchSchema:
		return "unsupported_batch_schema", 400
	case len(b.Revision) != 64 || strings.Trim(b.Revision, "0123456789abcdef") != "":
		return "invalid_batch_revision", 400
	case legacy && b.Revision != revision:
		return "revision_mismatch", 409
	}
	if len(b.Observations) > 100 || len(b.Observations) == 0 && b.Heartbeat == nil {
		return "invalid_batch_size", 400
	}
	validTime := func(t time.Time) bool {
		return !t.IsZero() && !t.After(now.Add(30*time.Second)) && !t.Before(now.Add(-maxAge))
	}
	for _, v := range b.Observations {
		x, ok := g.checks[v.Check]
		if !ok || x.Observer != observer || v.Observer != observer {
			return "observer_scope_violation", 403
		}
		if v.Project != b.Project || legacy && (v.Revision != b.Revision || v.Contract != "") {
			return "observation_contract_mismatch", 409
		}
		if !legacy {
			if v.Revision != "" || v.Contract != g.contracts[x.ID] {
				return "contract_mismatch", 409
			}
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
		if code := config.ObservationBounds(v, time.Duration(x.TimeoutSeconds+5)*time.Second); code != "" {
			return code, 400
		}
	}
	if h := b.Heartbeat; h != nil {
		if h.Observer != observer {
			return "heartbeat_scope_violation", 403
		}
		if !config.ID(h.Boot) || !validTime(h.SampledAt) || !h.ReceivedAt.IsZero() || h.Contracts != "" && (len(h.Contracts) != 64 || strings.Trim(h.Contracts, "0123456789abcdef") != "") {
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
