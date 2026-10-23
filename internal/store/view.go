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
	rows, carries, beats, _, _, _ := s.EvaluationViewWithExternal(0, true)
	return rows, carries, beats
}

// EvaluationView atomically captures both versions and their state. A heartbeat
// update copies only liveness; it does not visit retained observation history.
func (s *Store) EvaluationView(known uint64, force bool) ([]model.Observation, []engine.Carry, map[string]model.Heartbeat, uint64, uint64) {
	rows, carries, beats, _, version, evidenceVersion := s.EvaluationViewWithExternal(known, force)
	return rows, carries, beats, version, evidenceVersion
}

// EvaluationViewWithExternal is EvaluationView plus the persisted external
// acceptance state needed to judge reported evidence independently. When the
// evidence version is unchanged, external state is omitted with rows/carries;
// the caller keeps its prepared evidence and refreshes only heartbeats.
func (s *Store) EvaluationViewWithExternal(known uint64, force bool) ([]model.Observation, []engine.Carry, map[string]model.Heartbeat, *model.ExternalState, uint64, uint64) {
	s.mu.Lock()
	version, evidenceVersion := s.version, s.evidenceVersion
	beats := make(map[string]model.Heartbeat, len(s.beats))
	for k, v := range s.beats {
		beats[k] = model.CloneHeartbeat(v)
	}
	if !force && known == evidenceVersion {
		s.mu.Unlock()
		return nil, nil, beats, nil, version, evidenceVersion
	}
	external := model.CloneExternalState(s.external)
	// Raw history is usually already represented by tails. Reserve only
	// their size; fallback rows grow this slice in proportion to actual output.
	count := 0
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
		// Attempts and reports, including older tail entries, must not expose
		// pointers into the persisted observation graph.
		v.External = model.CloneExternalObservation(v.External)
	}
	return out, carries, beats, external, version, evidenceVersion
}

// Version changes whenever evidence used for state evaluation changes. It is
// process-local; reloads are additionally separated by the API's generation.
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}
