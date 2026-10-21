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
	rows, carries, beats, _, _ := s.EvaluationView(0, true)
	return rows, carries, beats
}

// EvaluationView atomically captures both versions and their state. A heartbeat
// update copies only liveness; it does not visit retained observation history.
func (s *Store) EvaluationView(known uint64, force bool) ([]model.Observation, []engine.Carry, map[string]model.Heartbeat, uint64, uint64) {
	s.mu.Lock()
	version, evidenceVersion := s.version, s.evidenceVersion
	beats := make(map[string]model.Heartbeat, len(s.beats))
	for k, v := range s.beats {
		beats[k] = model.CloneHeartbeat(v)
	}
	if !force && known == evidenceVersion {
		s.mu.Unlock()
		return nil, nil, beats, version, evidenceVersion
	}
	count := len(s.rows)
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
	return out, carries, beats, version, evidenceVersion
}

// Version changes whenever evidence used for state evaluation changes. It is
// process-local; reloads are additionally separated by the API's generation.
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}
