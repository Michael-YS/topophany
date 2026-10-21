package store

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

var ErrScopeCapacity = errors.New("observer storage quota exhausted")

// SetObservers installs trusted scope allocation before serving API requests.
// Accounting is reconstructed from durable receipts and retained rows at Open.
func (s *Store) SetObservers(ids []string) {
	s.SetAllocation(ids, nil)
}

// SetAllocation installs weights from an already validated topology.
func (s *Store) SetAllocation(ids []string, weights map[string]int) {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.historyDirty = true
	s.options.Observers = append([]string(nil), ids...)
	s.options.ObserverWeights = maps.Clone(weights)
	if s.options.ObserverWeights == nil {
		s.options.ObserverWeights = map[string]int{}
	}
	s.options.weightTotal = 0
	for _, id := range ids {
		w := max(weights[id], 1)
		s.options.ObserverWeights[id] = w
		s.options.weightTotal += int64(w)
	}
}

func receiptObserver(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ".")
}

func (s *Store) checkScopeCapacity(b model.Batch, sizes map[string]int64) error {
	n := len(s.options.Observers)
	if n == 0 {
		return nil
	} // standalone store callers may choose global limits
	allowed := map[string]bool{}
	for _, id := range s.options.Observers {
		allowed[id] = true
	}
	counts := map[string]int{}
	for _, v := range b.Observations {
		if !allowed[v.Observer] {
			return ErrScopeCapacity
		}
		counts[v.Observer]++
	}
	// Reserve a heartbeat slot for every configured observer, even when all
	// observation allocations are full. Boot changes replace, not grow, a slot.

	for id, count := range counts {
		byteLimit := s.options.byteShare(id)
		receiptLimit := int(s.options.share(int64(s.options.MaxReceipts), id))
		if s.scopeReceipts[id]+count > receiptLimit || s.scopeBytes[id]+sizes[id] > byteLimit {
			return ErrScopeCapacity
		}
	}
	if b.Heartbeat != nil && !allowed[b.Heartbeat.Observer] {
		return ErrScopeCapacity
	}
	return nil
}

func observationSize(v model.Observation) int64 {
	raw, _ := json.Marshal(v)
	return int64(len(raw))
}
