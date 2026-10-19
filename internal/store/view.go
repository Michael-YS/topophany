package store

import (
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// StateView copies evaluation scalars under lock, then detaches only the latest
// diagnostics outside the writer lock. It neither sorts nor serializes global
// history, and never visits receipts. Bounded tails, with
// what samples trimmed from the tails left in their carries, preserve
// hysteresis however long ago the samples that confirmed a failure were
// taken (see engine.BuildCarried).
func (s *Store) StateView() ([]model.Observation, []engine.Carry, map[string]model.Heartbeat) {
	s.mu.Lock()
	count := 0
	for _, v := range s.rows {
		if v.Contract == "" || !s.current(v) {
			count++
		}
	}
	for _, tail := range s.tails {
		count += len(tail)
	}
	rows := make([]model.Observation, 0, count)
	// Schema 1 observations cannot be folded into a carry. Also retain
	// history when the caller has not installed the contracts needed for
	// tails (or judges it under a different generation).
	for _, v := range s.rows {
		if v.Contract == "" || !s.current(v) {
			rows = append(rows, v)
		}
	}
	for _, tail := range s.tails {
		rows = append(rows, tail...)
	}
	carries := make([]engine.Carry, 0, len(s.carries))
	for _, k := range s.carries {
		carries = append(carries, k)
	}
	beats := make(map[string]model.Heartbeat, len(s.beats))
	for k, v := range s.beats {
		beats[k] = model.CloneHeartbeat(v)
	}
	s.mu.Unlock()

	seen := make(map[string]bool, len(rows))
	latest := map[string]int{}
	out := rows[:0]
	for _, v := range rows {
		if seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		key := tailKey(v)
		if i, ok := latest[key]; !ok || before(out[i], v) {
			latest[key] = len(out)
		}
		out = append(out, v)
	}
	clear(rows[len(out):])
	for i := range out {
		v := &out[i]
		if latest[tailKey(*v)] == i {
			*v = model.SummaryObservation(*v)
		} else {
			v.Facts = nil
			v.Timings = nil
			v.Detail = ""
		}
	}
	return out, carries, beats
}
