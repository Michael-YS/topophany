package store

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

var ErrScopeCapacity = errors.New("observer storage quota exhausted")

// SetObservers installs trusted scope allocation before serving API requests.
// Accounting is reconstructed from durable receipts and retained rows at Open.
func (s *Store) SetObservers(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.Observers = append([]string(nil), ids...)
}

func receiptObserver(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ".")
}

func (s *Store) checkScopeCapacity(b model.Batch) error {
	n := len(s.options.Observers)
	if n == 0 {
		return nil
	} // standalone store callers may choose global limits
	allowed := map[string]bool{}
	for _, id := range s.options.Observers {
		allowed[id] = true
	}
	counts, sizes := map[string]int{}, map[string]int64{}
	for _, v := range b.Observations {
		if !allowed[v.Observer] {
			return ErrScopeCapacity
		}
		counts[v.Observer]++
		// The transaction has already been encoded and detached. Use the same
		// conservative receipt/row reservation as global accounting.
		sizes[v.Observer] += observationSize(v) + 1024
	}
	// Reserve a heartbeat slot for every configured observer, even when all
	// observation allocations are full. Boot changes replace, not grow, a slot.
	byteLimit := (s.options.MaxStateBytes - 1024 - int64(n)*2048) / int64(n)
	receiptLimit := s.options.MaxReceipts / n
	for id, count := range counts {
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
