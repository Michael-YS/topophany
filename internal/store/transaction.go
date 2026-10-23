package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// admittedBatch owns normalized, deduplicated observations under the installed
// topology. Retention pruning is independent of admitting the new transaction.
type admittedBatch struct {
	batch      model.Batch
	encodings  map[string]rowEncoding
	receivedAt time.Time
}

// preparedWrite has passed capacity admission and owns the exact journal bytes
// and external-state delta to publish after durability. Neither escapes Put.
type preparedWrite struct {
	batch     model.Batch
	encodings map[string]rowEncoding
	external  *externalCommit
	record    []byte
}

// Put validates all duplicate IDs before writing one atomic journal transaction.
// A returned success always follows fsync. An I/O error poisons the store until
// restart, because the durability of that last transaction may be uncertain.
func (s *Store) Put(b model.Batch) error {
	// Encoding still validates the transaction and its wire budget.
	payload, e := json.Marshal(b)
	if e != nil {
		return e
	}
	if len(payload) > 1<<20 {
		return errors.New("transaction exceeds 1 MiB")
	}
	b = model.CloneBatch(b)
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	// A format migration installs a synchronous checkpoint. Finish any old
	// background snapshot before preparing this transaction: otherwise that
	// snapshot could overwrite the new barrier or retain a stale WAL boundary.
	// waitDurable releases both locks, so admission and receipt checks must
	// happen only after it returns under the current store/context state.
	if s.bytes >= s.options.MaxJournalBytes || hasExternalObservation(b.Observations) && s.format < 2 {
		s.waitDurable()
	}
	if s.closed {
		return errors.New("store closed")
	}
	if s.poisoned != nil {
		return s.poisoned
	}

	admitted, e := s.admitBatchLocked(b)
	if e != nil {
		return e
	}
	if e = s.ensureExternalFormatLocked(admitted.batch); e != nil {
		return e
	}
	tx, e := s.prepareWriteLocked(admitted)
	if e != nil || len(tx.record) == 0 {
		return e
	}
	if e = s.appendJournalLocked(tx.record); e != nil {
		return e
	}
	if e = s.apply(tx.batch, tx.encodings, tx.external); e != nil {
		s.poisoned = e
		return e
	}
	s.prune()
	if s.bytes >= s.checkpointThreshold() {
		s.startCheckpoint()
	}
	return nil
}

// All preparation methods require both commitMu and mu. They preserve the
// generation captured after waitDurable; no other writer can intervene.
func (s *Store) admitBatchLocked(b model.Batch) (admittedBatch, error) {
	now, future := s.admissionTimesLocked()
	// External observations are normalized and live-admitted against the
	// installed bindings before identity hashing. The API and local collectors
	// thus take the same decision, and an unnormalized direct writer cannot
	// poison the persisted cursor.
	for i := range b.Observations {
		if e := s.admitExternalLocked(&b.Observations[i], now, future); e != nil {
			return admittedBatch{}, e
		}
	}
	hashes := make([]digest, len(b.Observations))
	for i, v := range b.Observations {
		hashes[i] = observationHash(v)
	}
	// A heartbeat that would change nothing, such as one sent again, is
	// not written: it would cost a journal write and an fsync, under the
	// lock every observer's ingestion waits on, for nothing apply keeps.
	if b.Heartbeat != nil && !s.advances(*b.Heartbeat, b.Project, b.Revision) {
		b.Heartbeat = nil
	}
	pending := map[string]rowEncoding{}
	kept := make([]model.Observation, 0, len(b.Observations))
	for i, v := range b.Observations {
		if v.ID == "" || v.Check == "" || v.SampledAt.IsZero() {
			return admittedBatch{}, errors.New("observation identity and timestamp required")
		}
		h := hashes[i]
		if old, ok := s.existingHashLocked(v.ID, pending); ok {
			if old != h {
				return admittedBatch{}, ErrConflict
			}
			continue
		}
		if s.expired(v.ID) {
			// An expired receipt's sample is past the ingestion window, so
			// this is its ID reused for something else.
			return admittedBatch{}, ErrConflict
		}
		pending[v.ID] = rowEncoding{Hash: h}
		v.ReceivedAt = now
		kept = append(kept, v)
	}
	s.prune()
	b.Observations = kept
	if b.Heartbeat != nil {
		b.Heartbeat.ReceivedAt = now
	}
	return admittedBatch{b, pending, now}, nil
}

func (s *Store) existingHashLocked(id string, pending map[string]rowEncoding) (digest, bool) {
	if r, ok := s.seen[id]; ok {
		return r.Hash, true
	}
	if r, ok := pending[id]; ok {
		return r.Hash, true
	}
	r, ok := s.rowHashes[id]
	return r, ok
}

func (s *Store) ensureExternalFormatLocked(b model.Batch) error {
	// No external record may enter the journal before a checkpoint schema an
	// older binary refuses to read is durable. The existing checkpoint path
	// performs the atomic replacement, file sync and directory sync; a failure
	// leaves the transaction unacknowledged and requires restart.
	if hasExternalObservation(b.Observations) && s.format < 2 {
		s.format = 2
		if e := s.checkpoint(); e != nil {
			s.poisoned = fmt.Errorf("external checkpoint format barrier: %w", e)
			return s.poisoned
		}
	}
	return nil
}

func (s *Store) prepareWriteLocked(a admittedBatch) (preparedWrite, error) {
	b, pending, now := a.batch, a.encodings, a.receivedAt
	// Bound bytes as well as row counts. Otherwise valid large facts can
	// create a checkpoint larger than the restart decoder can ever read.
	projected := s.stateBytes
	sizes := make(map[string]int64)
	for _, v := range b.Observations {
		bytes := observationSize(v)
		size := bytes + 1024
		encoded := pending[v.ID]
		encoded.Bytes = bytes + 1
		pending[v.ID] = encoded
		projected += size
		sizes[v.Observer] += size
	}
	if b.Heartbeat != nil {
		raw, _ := json.Marshal(b.Heartbeat)
		projected += int64(len(raw)) + 1024
		projected -= s.beatBytes[b.Heartbeat.Observer]
	}
	var externalNext *externalCommit
	var externalScopeDelta map[string]int64
	externalChanged := hasExternalObservation(b.Observations)
	if externalChanged {
		commit, e := s.mergeExternalLocked(b.Observations)
		if e != nil {
			return preparedWrite{}, e
		}
		externalNext = commit
		if commit != nil {
			projected += commit.size - s.externalBytes
			externalScopeDelta = make(map[string]int64)
			for observer, size := range commit.observerBytes {
				if delta := size - s.externalObserverBytes[observer]; delta != 0 {
					externalScopeDelta[observer] = delta
				}
			}
		}
	}
	// checkScopeCapacity runs after the external merge so the observer share
	// includes this transaction's report state as well as its raw samples.
	if err := s.checkScopeCapacity(b, sizes, externalScopeDelta); err != nil {
		return preparedWrite{}, err
	}
	if len(s.seen)+len(pending) > s.options.MaxReceipts || projected > s.options.MaxStateBytes {
		return preparedWrite{}, ErrCapacity
	}
	if len(b.Observations) == 0 && b.Heartbeat == nil {
		return preparedWrite{}, nil
	}
	if len(b.Observations) == 0 && b.Heartbeat != nil {
		// A collector clock stepped back is not a heartbeat arriving early:
		// refusing until it caught up would read as the observer falling
		// silent. The admitted heartbeat restarts the cadence at the new time.
		if old, ok := s.beats[b.Heartbeat.Observer]; ok && !now.Before(old.ReceivedAt) && now.Sub(old.ReceivedAt) < heartbeatWriteInterval {
			return preparedWrite{}, ErrHeartbeatRate
		}
	}
	record, e := wrap(b)
	if e != nil {
		return preparedWrite{}, e
	}
	record = append(record, '\n')
	return preparedWrite{b, pending, externalNext, record}, nil
}

// Only journal IO releases mu. commitMu continues to serialize writers and
// checkpoint installation. Uncertain durability poisons the store before any
// prepared state can be published to readers.
func (s *Store) appendJournalLocked(record []byte) error {
	// Readers may proceed while this record is made durable. commitMu still
	// keeps the next writer, and the checkpoint installer, off this file.
	s.mu.Unlock()
	started := time.Now()
	n, e := s.file.Write(record)
	ioaccount.Journal.Add(n, time.Since(started), e)
	if e == nil && n != len(record) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = syncFile(s.file)
	}
	s.mu.Lock()
	if e != nil {
		s.poisoned = fmt.Errorf("journal durability failure; restart required: %w", e)
		return s.poisoned
	}
	s.bytes += int64(n)
	return nil
}
