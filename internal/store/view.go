package store

import "github.com/WhitenWhiten/topophany/internal/model"

// StateView copies evaluation scalars under lock, then detaches only the latest
// diagnostics outside the writer lock. It neither sorts nor serializes global
// history, and never visits receipts. Keeping the union of history and tails
// preserves hysteresis even when intermittent failures span more than 40 rows.
func (s *Store) StateView() ([]model.Observation, map[string]model.Heartbeat) {
	s.mu.Lock()
	rows := make([]model.Observation, 0, len(s.rows)+len(s.tails)*40)
	rows = append(rows, s.rows...)
	for _, tail := range s.tails {
		rows = append(rows, tail...)
	}
	beats := make(map[string]model.Heartbeat, len(s.beats))
	for k, v := range s.beats {
		beats[k] = v
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
	return out, beats
}
