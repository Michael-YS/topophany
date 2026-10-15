package rollup

import (
	"slices"
	"sort"
	"time"
)

const boundaryBytes = 64 // encoded timestamp, stale duration, class and delimiters

// SetReorderWindow retains the boundaries needed to correct samples within
// the collector's ingestion window. They share the rollup byte budget.
func (r *Store) SetReorderWindow(window time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reorder = max(1, window.Milliseconds())
}

func (r *Store) discardBoundaries(s *series, before int64) {
	s.correctionFrom = max(s.correctionFrom, before)
	// Keep the predecessor: its outcome can still cover a late sample's time.
	i := sort.Search(len(s.recent), func(i int) bool { return s.recent[i].at >= before })
	i = max(0, i-1)
	if i > 0 {
		s.recent = s.recent[i:]
		r.grow(s, -int64(i)*boundaryBytes)
	}
}

// insert changes only the two intervals adjacent to a new boundary. The
// newest interval remains open; all earlier intervals are already booked.
// Equal timestamps keep the first outcome and count both samples, disclosing
// conflicting outcomes as unreconciled.
func (r *Store) insert(s *series, point open, now int64) bool {
	if s.open == nil || point.at > s.open.at {
		if s.open != nil {
			r.spend(s, s.open.at, min(point.at, s.open.at+s.open.stale), s.open.class, now)
		}
		s.open = &point
		s.recent = append(s.recent, point)
		r.grow(s, boundaryBytes)
		return true
	}
	if point.at < s.correctionFrom {
		return false
	}
	i := sort.Search(len(s.recent), func(i int) bool { return s.recent[i].at >= point.at })
	if i < len(s.recent) && s.recent[i].at == point.at {
		// Arrival order cannot resolve conflicting outcomes at the same
		// timestamp. Preserve the first and disclose the ambiguity.
		return s.recent[i] == point
	}
	if i == len(s.recent) {
		return false
	}
	next := s.recent[i]
	if i > 0 {
		previous := s.recent[i-1]
		r.unspend(s, point.at, min(next.at, previous.at+previous.stale), previous.class)
	}
	r.spend(s, point.at, min(next.at, point.at+point.stale), point.class, now)
	s.recent = slices.Insert(s.recent, i, point)
	r.grow(s, boundaryBytes)
	return true
}

// unspend removes only existing credited time; it never recreates a bucket
// evicted by the budget. Correction boundaries advance with those evictions.
func (r *Store) unspend(s *series, from, to int64, class int) {
	for i := sort.Search(len(s.buckets), func(i int) bool { return s.buckets[i].end() > from }); i < len(s.buckets) && s.buckets[i].start < to; i++ {
		b := &s.buckets[i]
		if n := min(to, b.end()) - max(from, b.start); n > 0 {
			b.ms[class] -= n
		}
	}
}
