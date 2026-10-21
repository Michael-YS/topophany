package store

import "time"

// AdmissionTimes lets the API and direct local ingestion advance the same
// trusted clock under the store lock. The caller supplies collector clocks,
// never a timestamp from an observation. Sharing state also handles a rollback
// between API validation and journal admission, including the first write.
func (s *Store) AdmissionTimes(wall, tick time.Time) (time.Time, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return wall, s.admissionClock.Advance(wall, tick)
}

func (s *Store) admissionTimesLocked() (time.Time, time.Time) {
	wall := s.options.Now().UTC()
	tick := time.Now()
	if s.options.MonotonicNow != nil {
		tick = s.options.MonotonicNow()
	}
	return wall, s.admissionClock.Advance(wall, tick)
}
