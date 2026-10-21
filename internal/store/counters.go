package store

import (
	"encoding/json"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func (s *Store) adjustBytes(observer string, delta int64) {
	s.stateBytes += delta
	s.scopeBytes[observer] += delta
	if s.scopeBytes[observer] == 0 {
		delete(s.scopeBytes, observer)
	}
}

func (s *Store) replaceBeat(h model.Heartbeat) {
	raw, _ := json.Marshal(h)
	size := int64(len(raw)) + 1024
	s.stateBytes += size - s.beatBytes[h.Observer]
	s.beatBytes[h.Observer] = size
	s.beats[h.Observer] = h
}

// Recovery reconstructs counters once; live writes update only changed owners.
func (s *Store) recountBytes() {
	s.stateBytes = 1024 + int64(len(s.carries))*carryBytes + s.externalBytes
	s.scopeBytes = map[string]int64{}
	for id, n := range s.scopeReceipts {
		s.adjustBytes(id, int64(n)*512)
	}
	for id, q := range s.floorQueues {
		s.adjustBytes(id, int64(q.Len())*floorBytes)
	}
	for id, size := range s.rowScope {
		s.adjustBytes(id, size)
	}
	s.beatBytes = map[string]int64{}
	for _, h := range s.beats {
		s.replaceBeat(h)
	}
}

func (s *Store) recountRecent(now time.Time) {
	s.recent = map[string]int{}
	s.recentHead = s.receiptHead
	cutoff := now.Add(-time.Hour)
	for s.recentHead < len(s.receiptQueue) {
		r, ok := s.seen[s.receiptQueue[s.recentHead]]
		if ok && !r.At.Before(cutoff) {
			break
		}
		s.recentHead++
	}
	for _, id := range s.receiptQueue[s.recentHead:] {
		if r, ok := s.seen[id]; ok {
			s.recent[r.Observer]++
		}
	}
	s.recentAt = now
}

// The cursor expires each receipt once, rather than scanning the last hour on
// every read. A clock rollback rebuilds the display rate from retained receipts;
// neither this rate nor its clock handling participates in admission quotas.
func (s *Store) advanceRecent(now time.Time) {
	if s.recent == nil || now.Before(s.recentAt) {
		s.recountRecent(now)
	}
	cutoff := now.Add(-time.Hour)
	for s.recentHead < len(s.receiptQueue) {
		r, ok := s.seen[s.receiptQueue[s.recentHead]]
		if ok && !r.At.Before(cutoff) {
			break
		}
		s.recentHead++
		if ok {
			s.recent[r.Observer]--
			if s.recent[r.Observer] == 0 {
				delete(s.recent, r.Observer)
			}
		}
	}
	s.recentAt = now
}
