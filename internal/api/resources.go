package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func (s *Server) acquire(scope, role string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[scope] >= 2 {
		return false
	}
	slots := s.slots
	if role == "viewer" {
		slots = s.viewerSlots
	}
	select {
	case slots <- struct{}{}:
		s.inflight[scope]++
		return true
	default:
		return false
	}
}
func (s *Server) release(scope, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight[scope]--
	slots := s.slots
	if role == "viewer" {
		slots = s.viewerSlots
	}
	<-slots
}

func (s *Server) writeSnapshot(w http.ResponseWriter, g *generation, view, principal string) {
	raw, status, code := s.cachedSnapshot(g, view, principal)
	if status != http.StatusOK {
		if status == 429 || code == "topology_reloaded" {
			w.Header().Set("Retry-After", "1")
		}
		writeError(w, status, code)
		return
	}
	// A slow reader must not retain the cache/build mutex.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// cachedSnapshot is the snapshot of view under g, the generation its
// request was admitted under.
func (s *Server) cachedSnapshot(g *generation, view, principal string) ([]byte, int, string) {
	// Reload takes this lock before changing any snapshot dependency. Refuse
	// an old authority before reading storage or ledger metadata, even on a
	// cache hit, and keep the generation stable until capture is complete.
	s.reloadMu.RLock()
	defer s.reloadMu.RUnlock()
	if g != s.generation() {
		return nil, http.StatusServiceUnavailable, "topology_reloaded"
	}
	// Capture bounded store tails once per second. Cache at most eight observer
	// projections (32 MiB total) so switching views cannot amplify store reads.
	// Waiting readers never queue expensive work, and writes use a separate mutex.
	// What is cached was built under one generation and is served under no
	// other.
	if !s.snapshotMu.TryLock() {
		return nil, 429, "snapshot_busy"
	}
	defer s.snapshotMu.Unlock()
	if s.snapshotCache == nil || s.snapshotGen != g || s.Now().Sub(s.snapshotAt) >= time.Second {
		s.snapshotGen = g
		s.snapshotRows, s.snapshotCarries, s.snapshotBeats = s.Store.StateView()
		s.snapshotAt = s.Now()
		s.snapshotStorage = s.Store.Usage()
		s.snapshotAvailable = nil
		s.snapshotCache = map[string][]byte{}
		s.snapshotCacheBytes = 0
		s.snapshotMisses = map[string]int{}
		s.snapshotBuilds = 0
	}
	if raw, ok := s.snapshotCache[view]; ok {
		return raw, http.StatusOK, ""
	}
	if s.snapshotMisses[principal] >= 2 || s.snapshotBuilds >= 8 {
		return nil, 429, "snapshot_rate_limited"
	}
	s.snapshotMisses[principal]++
	s.snapshotBuilds++
	snap := s.buildSnapshot(g, view, s.snapshotRows, s.snapshotCarries, s.snapshotBeats, s.snapshotAt)
	usage := model.StorageUsage(s.snapshotStorage)
	usage.Observers = slices.Clone(usage.Observers)
	if view != "" {
		usage.Observers = slices.DeleteFunc(usage.Observers, func(o model.StorageObserverUsage) bool { return o.Observer != view })
	}
	snap.Storage = &usage
	if !s.ExposeIngressSources {
		snap.Ingress = pseudonymousIngress(snap.Ingress, s.ingressSalt[:])
	}
	if s.Ledger != nil {
		snap.Ledger = s.Ledger.Recent(view, s.snapshotAt)
	}
	if s.snapshotAvailable == nil {
		// Every view of this moment shares one reading of the rollups.
		s.snapshotAvailable = map[string][]model.Availability{}
		for id, contract := range g.contracts {
			s.snapshotAvailable[id] = s.Store.Availability(id, contract, s.snapshotAt)
		}
	}
	for i := range snap.Checks {
		snap.Checks[i].Availability = s.snapshotAvailable[snap.Checks[i].ID]
	}
	raw, err := summaryJSON(snap)
	if err != nil {
		return nil, 503, "snapshot_budget_exceeded"
	}
	for key, old := range s.snapshotCache {
		if s.snapshotCacheBytes+len(raw) <= 32<<20 && len(s.snapshotCache) < 8 {
			break
		}
		delete(s.snapshotCache, key)
		s.snapshotCacheBytes -= len(old)
	}
	s.snapshotCache[view] = raw
	s.snapshotCacheBytes += len(raw)
	return raw, http.StatusOK, ""
}

var errSnapshotBudget = errors.New("snapshot budget exceeded")

func summaryJSON(s model.Snapshot) ([]byte, error) {
	// Redact before deriving the viewer's identity, including nested last
	// observations. Keep the execution snapshot and stored evidence unchanged.
	s.Topology = config.Public(s.Topology)
	s.Revision = config.Revision(s.Topology)
	s.Checks = slices.Clone(s.Checks)
	// History remains the source of full diagnostic evidence.
	s.DetailsOmitted = true
	for i := range s.Checks {
		c := &s.Checks[i]
		c.Reason = model.SummaryText(c.Reason)
		if c.Last != nil {
			v := model.SummaryObservation(*c.Last)
			v.Contract, v.Revision = c.Contract, ""
			c.Last = &v
		}
	}
	references := 8192
	trim := func(ids []string) []string {
		n := min(len(ids), 32, references)
		references -= n
		return ids[:n]
	}
	for i := range s.Incidents {
		s.Incidents[i].Checks = trim(s.Incidents[i].Checks)
		s.Incidents[i].CounterEvidence = trim(s.Incidents[i].CounterEvidence)
	}
	s.Ledger = slices.Clone(s.Ledger)
	for i := range s.Ledger {
		s.Ledger[i].Checks = trim(s.Ledger[i].Checks)
	}
	for _, states := range [][]model.EntityState{s.Nodes, s.Endpoints, s.Services, s.Paths, s.Links} {
		for i := range states {
			states[i].Reason = model.SummaryText(states[i].Reason)
			states[i].Checks = trim(states[i].Checks)
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	// Over budget, availability goes first, as rollups can be read on
	// their own; then the last observations, as history can.
	if len(raw) > model.MaxSnapshotBytes || secure.JSONBudget(raw) != nil {
		for i := range s.Checks {
			s.Checks[i].Availability = nil
		}
		raw, err = json.Marshal(s)
	}
	if err == nil && (len(raw) > model.MaxSnapshotBytes || secure.JSONBudget(raw) != nil) {
		for i := range s.Checks {
			s.Checks[i].Last = nil
			s.Checks[i].Reason = "summary budget reached; consult check history"
		}
		raw, err = json.Marshal(s)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > model.MaxSnapshotBytes || secure.JSONBudget(raw) != nil {
		return nil, errSnapshotBudget
	}
	return raw, nil
}
