package store

import (
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Called with mu held. At most one detached checkpoint exists. Observation
// values and heartbeat payloads are immutable after admission; containers and
// mutable rollups are copied. The WAL's byte boundary names exactly this state.
func (s *Store) startCheckpoint() {
	if s.checkpointDone != nil {
		return
	}
	s.settled = s.options.Now()
	s.rollups.Settle(s.settled)
	snapshot := &Store{
		rows: slices.Clone(s.rows), tails: make(map[string][]model.Observation, len(s.tails)),
		carries: maps.Clone(s.carries), seen: maps.Clone(s.seen), beats: maps.Clone(s.beats),
		floors: maps.Clone(s.floors), floorAt: maps.Clone(s.floorAt), rollups: s.rollups.Clone(),
	}
	for k, tail := range s.tails {
		snapshot.tails[k] = slices.Clone(tail)
	}
	boundary := s.bytes
	done := make(chan struct{})
	s.checkpointDone = done
	go func() {
		err := snapshot.writeCheckpoint(filepath.Join(s.dir, "checkpoint.json"))
		s.mu.Lock()
		defer s.mu.Unlock()
		if err == nil {
			s.checkpointSize = snapshot.checkpointSize
			err = s.retainJournalTail(boundary)
		}
		if err != nil {
			s.poisoned = err
		}
		s.checkpointDone = nil
		close(done)
	}()
}

// The durable checkpoint covers [0,boundary). Replace the WAL atomically with
// only the suffix appended while it was written. A crash before replacement
// replays the old prefix idempotently; after replacement every new ACKed sample
// is still present. Never truncate the live journal to the captured boundary.
// Called with mu held, so the suffix cannot change during its bounded copy.
func (s *Store) retainJournalTail(boundary int64) error {
	tail := s.bytes - boundary
	if tail < 0 {
		return errors.New("checkpoint journal boundary moved")
	}
	path := filepath.Join(s.dir, "journal.jsonl")
	closed := false
	err := secure.AtomicWriteStreamMeasured(path, 0600, &ioaccount.Journal, func(w io.WriteSeeker) error {
		_, err := io.Copy(w, io.NewSectionReader(s.file, boundary, tail))
		if err != nil {
			return err
		}
		// Windows replacement requires closing the original handle first. The old
		// WAL remains durable if publication of its replacement fails.
		closed = true
		return s.file.Close()
	})
	if !closed {
		_ = s.file.Close()
	}
	var openErr error
	s.file, openErr = os.OpenFile(path, os.O_RDWR, 0600)
	if openErr != nil {
		return errors.Join(err, openErr)
	}
	if _, openErr = s.file.Seek(0, io.SeekEnd); openErr != nil {
		return errors.Join(err, openErr)
	}
	if err == nil {
		s.bytes = tail
	}
	return err
}

// Wait without holding the store lock. Put uses this only at the journal
// ceiling; explicit checkpoints and Close also wait for in-flight publication.
func (s *Store) waitCheckpoint() {
	for s.checkpointDone != nil {
		done := s.checkpointDone
		s.mu.Unlock()
		<-done
		s.mu.Lock()
	}
}
