// Package store implements a single-writer, checksummed, fsync-before-ack
// journal and atomic checkpoints. It requires a local filesystem, never a
// shared NFS/SMB volume. Collectors replicate observations, not database files.
// Besides raw history, the store keeps rollups of every sample it accepts
// (see package rollup), written with each checkpoint and rebuilt from the
// journal after it, so they count each sample exactly once.
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/rollup"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var ErrConflict = errors.New("observation ID reused with different contents")
var ErrCapacity = errors.New("receipt capacity reached; collection paused instead of losing deduplication")

// Options bound the store. Revision and Contracts, when set, are the
// topology a collector counts samples under (see config.Counted): only
// their samples keep state tails. Stale is how long each check's sample
// stays current, which is as long as its outcome stands in the rollups.
// Retention is also the ingestion window: a receipt is kept until no
// sample it could match can be accepted, an hour after it.
type Options struct {
	Observers       []string
	Revision        string
	Contracts       map[string]string
	Stale           map[string]time.Duration
	MaxHistory      int
	MaxReceipts     int
	Retention       time.Duration
	CheckpointBytes int64
	MaxStateBytes   int64
	RollupBytes     int64
	Now             func() time.Time
}

// receiptGrace is how long a receipt outlives the ingestion window.
const receiptGrace = time.Hour

// floorBytes is what a boot's floor costs in its observer's quota.
const floorBytes = 128

// carryBytes is what a series' carry costs in the state budget.
const carryBytes = 512

type receipt struct {
	Observer string    `json:"observer,omitempty"`
	Hash     string    `json:"hash"`
	At       time.Time `json:"at"`
}

// diskState is a checkpoint. Floors are, for each boot (observer.boot)
// whose receipts expired, the highest sequence among them: an ID at or
// below its boot's floor stays bound and is never accepted again.
type diskState struct {
	Schema  int                        `json:"schema"`
	Rows    []model.Observation        `json:"rows"`
	Seen    map[string]receipt         `json:"seen"`
	Beats   map[string]model.Heartbeat `json:"beats"`
	Floors  map[string]uint64          `json:"floors,omitempty"`
	Rollups *rollup.Store              `json:"rollups,omitempty"`
	Carries map[string]engine.Carry    `json:"carries,omitempty"`
}
type envelope struct {
	SHA256  string          `json:"sha256"`
	Payload json.RawMessage `json:"payload"`
}
type Store struct {
	mu            sync.Mutex
	dir           string
	file          *os.File
	unlock        func() error
	options       Options
	rows          []model.Observation
	tails         map[string][]model.Observation
	carries       map[string]engine.Carry // what samples trimmed from each tail left
	seen          map[string]receipt
	beats         map[string]model.Heartbeat
	bytes         int64
	closed        bool
	poisoned      error
	rowSizes      map[string]int64
	stateBytes    int64
	rowHashes     map[string]string
	rowObservers  map[string]string
	scopeBytes    map[string]int64
	scopeReceipts map[string]int
	floors        map[string]uint64
	rollups       *rollup.Store
	settled       time.Time // when the rollups last settled

	// Receipts in the order they were taken, from receiptHead on, so the
	// expired ones are found at the front rather than by visiting them all;
	// and what the held rows add up to, in all and by observer. prune keeps
	// these up to date; recount derives them afresh.
	receiptQueue []string
	receiptHead  int
	rowBytes     int64
	rowScope     map[string]int64
	rowsDropped  bool // a tail let rows go since the last prune
}

func Open(dir string, o Options) (s *Store, err error) {
	o.Observers = append([]string(nil), o.Observers...)
	if o.MaxHistory == 0 {
		o.MaxHistory = 20000
	}
	if o.MaxReceipts == 0 {
		o.MaxReceipts = 500000
	}
	if o.Retention == 0 {
		o.Retention = 24 * time.Hour
	}
	if o.CheckpointBytes == 0 {
		o.CheckpointBytes = 16 << 20
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxStateBytes == 0 {
		o.MaxStateBytes = 128 << 20
	}
	if o.RollupBytes == 0 {
		o.RollupBytes = 32 << 20
	}
	if o.MaxHistory < 1 || o.MaxReceipts < 1 || o.Retention < time.Minute || o.CheckpointBytes < 1024 || o.CheckpointBytes > 128<<20 || o.MaxStateBytes < 1024 || o.MaxStateBytes > 128<<20 || o.RollupBytes < rollup.MinLimit || o.RollupBytes > rollup.MaxLimit {
		return nil, errors.New("invalid storage limits")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	unlock, e := secure.Lock(filepath.Join(dir, "writer.lock"))
	if e != nil {
		return nil, e
	}
	s = &Store{dir: dir, unlock: unlock, options: o, tails: map[string][]model.Observation{}, carries: map[string]engine.Carry{}, seen: map[string]receipt{}, beats: map[string]model.Heartbeat{}, rowSizes: map[string]int64{}, rowHashes: map[string]string{}, rowObservers: map[string]string{}, floors: map[string]uint64{}, scopeReceipts: map[string]int{}, rowScope: map[string]int64{}, rollups: rollup.New(o.RollupBytes)}
	s.rollups.SetCurrent(s.currentKey)
	opened := s
	defer func() {
		if err != nil {
			if opened.file != nil {
				opened.file.Close()
			}
			unlock()
		}
	}()
	if b, e := secure.ReadBounded(filepath.Join(dir, "checkpoint.json"), 256<<20); e == nil {
		if len(b) > 256<<20 {
			return nil, errors.New("checkpoint limit exceeded")
		}
		payload, e := unwrap(b)
		if e != nil {
			return nil, fmt.Errorf("checkpoint corruption: %w", e)
		}
		state := diskState{Rollups: s.rollups}
		if e = json.Unmarshal(payload, &state); e != nil || state.Schema != 1 || state.Seen == nil || state.Beats == nil {
			return nil, errors.New("invalid checkpoint")
		}
		s.seen = state.Seen
		s.beats = state.Beats
		if state.Floors != nil {
			s.floors = state.Floors
		}
		// Carries first, so the rows they already hold are not folded again
		// as the tails are rebuilt.
		if state.Carries != nil {
			s.carries = state.Carries
		}
		for _, v := range state.Rows {
			s.addRow(v)
			// State tails outlive receipts. Restore their receipts during replay
			// so a crash after checkpoint replacement cannot add them twice.
			if _, ok := s.seen[v.ID]; !ok && !s.expired(v.ID) {
				s.seen[v.ID] = receipt{Hash: observationHash(v), At: v.ReceivedAt, Observer: v.Observer}
			}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	path := filepath.Join(dir, "journal.jsonl")
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return nil, errors.New("journal must be regular file")
	}
	s.file, e = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	st, e := s.file.Stat()
	if e != nil {
		return nil, e
	}
	if st.Size() > 256<<20 {
		return nil, errors.New("journal exceeds recovery budget")
	}
	reader := bufio.NewReaderSize(s.file, (2<<20)+1)
	offset := int64(0)
	for {
		line, e := reader.ReadSlice('\n')
		if len(line) > 2<<20 {
			return nil, errors.New("journal record too large")
		}
		if e == io.EOF {
			if len(line) > 0 {
				if er := s.file.Truncate(offset); er != nil {
					return nil, er
				}
				if er := s.file.Sync(); er != nil {
					return nil, er
				}
			}
			break
		}
		if e != nil {
			return nil, e
		}
		payload, e := unwrap(bytes.TrimSuffix(line, []byte{'\n'}))
		if e != nil {
			return nil, fmt.Errorf("journal corruption at byte %d", offset)
		}
		var batch model.Batch
		if e = json.Unmarshal(payload, &batch); e != nil {
			return nil, errors.New("invalid journal transaction")
		}
		if e = s.apply(batch, true); e != nil {
			return nil, e
		}
		offset += int64(len(line))
	}
	s.bytes = offset
	if _, e = s.file.Seek(0, io.SeekEnd); e != nil {
		return nil, e
	}
	s.recount()
	s.prune()
	return s, nil
}

// recount derives the receipt queue and the accounting of receipts and
// rows from what the store holds, as when it opens.
func (s *Store) recount() {
	s.receiptQueue, s.receiptHead = make([]string, 0, len(s.seen)), 0
	s.scopeReceipts = map[string]int{}
	for id, r := range s.seen {
		if r.Observer == "" {
			r.Observer = receiptObserver(id)
			s.seen[id] = r
		}
		s.scopeReceipts[r.Observer]++
		s.receiptQueue = append(s.receiptQueue, id)
	}
	slices.SortFunc(s.receiptQueue, func(a, b string) int {
		if c := s.seen[a].At.Compare(s.seen[b].At); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	s.rowBytes, s.rowScope = 0, map[string]int64{}
	for id, size := range s.rowSizes {
		s.rowBytes += size
		s.rowScope[s.rowObservers[id]] += size
	}
	s.rowsDropped = true
}

// receive records the receipt of a sample.
func (s *Store) receive(id string, r receipt) {
	s.seen[id] = r
	s.scopeReceipts[r.Observer]++
	s.receiptQueue = append(s.receiptQueue, id)
}
func wrap(v any) ([]byte, error) {
	p, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return json.Marshal(envelope{SHA256: secure.Hash(string(p)), Payload: p})
}
func unwrap(b []byte) (json.RawMessage, error) {
	var e envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	if e.SHA256 != secure.Hash(string(e.Payload)) {
		return nil, errors.New("checksum mismatch")
	}
	return e.Payload, nil
}
func observationHash(v model.Observation) string {
	v.ReceivedAt = time.Time{}
	b, _ := json.Marshal(v)
	return secure.Hash(string(b))
}

// tailKey is a check's series: its samples under one contract, or for
// schema 1 samples under one revision.
func tailKey(v model.Observation) string {
	if v.Contract != "" {
		return v.Contract + "/" + v.Check
	}
	return v.Revision + "/" + v.Check
}

// current reports whether v counts under the topology the store is told
// of; every sample does when it is told of none.
func (s *Store) current(v model.Observation) bool {
	if s.options.Revision == "" && s.options.Contracts == nil {
		return true
	}
	return config.Counted(v, s.options.Contracts, s.options.Revision)
}

// rollKey is the rollup series of a sample: its check under its contract.
// A schema 1 sample counted under the topology the store is told of joins
// its check's contract there; an uncounted one keeps its revision.
func (s *Store) rollKey(v model.Observation) string {
	if v.Contract == "" && v.Revision != "" && v.Revision == s.options.Revision {
		if c := s.options.Contracts[v.Check]; c != "" {
			return c + "/" + v.Check
		}
	}
	return tailKey(v)
}

// currentKey reports whether a rollup series is counted under the
// topology the store is told of. The rollups call it under s.mu.
func (s *Store) currentKey(key string) bool {
	if s.options.Revision == "" && s.options.Contracts == nil {
		return true
	}
	contract, check, ok := strings.Cut(key, "/")
	return ok && s.options.Contracts[check] == contract
}

// roll folds a newly accepted sample into its rollup series, which its
// observer owns.
func (s *Store) roll(v model.Observation) {
	s.rollups.Add(s.rollKey(v), v.Observer, v.SampledAt, v.Outcome, v.DurationMS, s.options.Stale[v.Check], s.options.Now())
}

// bootOf splits an observation ID, observer.boot.sequence, into its boot
// and its sequence.
func bootOf(id string) (string, uint64, bool) {
	i := strings.LastIndexByte(id, '.')
	if i < 0 || strings.IndexByte(id[:i], '.') <= 0 {
		return "", 0, false
	}
	seq, e := strconv.ParseUint(id[i+1:], 10, 64)
	return id[:i], seq, e == nil
}

// expired reports whether id's receipt expired and its boot's floor binds
// it: no sample with that ID is accepted again.
func (s *Store) expired(id string) bool {
	boot, seq, ok := bootOf(id)
	floor, held := s.floors[boot]
	return ok && held && seq <= floor
}

// SetCurrent tells the store the topology it now counts samples under, as
// after a reload: tails of series no longer counted go, and those counted
// again are rebuilt from the history still held. Rollups of series no
// longer counted are kept, and shed first when the rollups are full.
func (s *Store) SetCurrent(revision string, contracts map[string]string, stale map[string]time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.Revision = revision
	s.options.Contracts = maps.Clone(contracts)
	s.options.Stale = maps.Clone(stale)
	rows := s.allRows()
	s.tails = map[string][]model.Observation{}
	for _, v := range rows {
		if s.current(v) {
			key := tailKey(v)
			s.tails[key] = append(s.tails[key], v)
		}
	}
	for key, tail := range s.tails {
		s.tails[key] = s.trim(key, tail)
	}
	for key := range s.carries {
		if _, ok := s.tails[key]; !ok {
			delete(s.carries, key)
		}
	}
	s.prune()
}
func (s *Store) addRow(v model.Observation) {
	if _, ok := s.rowSizes[v.ID]; !ok {
		b, _ := json.Marshal(v)
		s.rowSizes[v.ID] = int64(len(b)) + 1
		s.rowHashes[v.ID] = observationHash(v)
		s.rowObservers[v.ID] = v.Observer
		s.rowBytes += s.rowSizes[v.ID]
		s.rowScope[v.Observer] += s.rowSizes[v.ID]
	}
	s.rows = append(s.rows, v)
	if !s.current(v) {
		return
	}
	key := tailKey(v)
	tail := append(s.tails[key], v)
	sort.Slice(tail, func(i, j int) bool { return before(tail[i], tail[j]) })
	s.tails[key] = s.trim(key, tail)
}

// tailLength is how many of its latest samples a series keeps for its state.
const tailLength = 40

// trim keeps the latest samples of a sorted tail and folds those it lets
// go into the series' carry, so its state outlives them. A sample its
// carry already holds, such as one arriving late, is not folded again.
func (s *Store) trim(key string, tail []model.Observation) []model.Observation {
	if len(tail) <= tailLength {
		return tail
	}
	s.rowsDropped = true
	k := s.carries[key]
	for _, v := range tail[:len(tail)-tailLength] {
		if v.Contract != "" && !k.Holds(v) {
			k.Fold(v, s.options.Stale[v.Check])
		}
	}
	if !k.At.IsZero() {
		s.carries[key] = k
	}
	return tail[len(tail)-tailLength:]
}
func before(a, b model.Observation) bool {
	if a.SampledAt.Equal(b.SampledAt) {
		if a.Observer == b.Observer && a.Boot == b.Boot {
			return a.Sequence < b.Sequence
		}
		return a.ID < b.ID
	}
	return a.SampledAt.Before(b.SampledAt)
}
func (s *Store) apply(b model.Batch, recovery bool) error {
	for _, v := range b.Observations {
		h := observationHash(v)
		if old, ok := s.seen[v.ID]; ok {
			if old.Hash != h {
				return ErrConflict
			}
			continue
		}
		if s.expired(v.ID) {
			// Recovery replays a record whose receipt has since expired.
			continue
		}
		s.receive(v.ID, receipt{Hash: h, At: v.ReceivedAt, Observer: v.Observer})
		s.addRow(v)
		s.roll(v)
	}
	if b.Heartbeat != nil && s.advances(*b.Heartbeat, b.Project, b.Revision) {
		h := *b.Heartbeat
		h.Project, h.Revision = b.Project, b.Revision
		s.beats[h.Observer] = h
	}
	_ = recovery
	return nil
}

// advances reports whether heartbeat h, sent under project and revision,
// replaces the one its observer has: it is the first, it comes under
// another project or revision, or it was taken later.
func (s *Store) advances(h model.Heartbeat, project, revision string) bool {
	old, ok := s.beats[h.Observer]
	return !ok || project != old.Project || revision != old.Revision || h.SampledAt.After(old.SampledAt)
}

// Put validates all duplicate IDs before writing one atomic journal transaction.
// A returned success always follows fsync. An I/O error poisons the store until
// restart, because the durability of that last transaction may be uncertain.
func (s *Store) Put(b model.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store closed")
	}
	if s.poisoned != nil {
		return s.poisoned
	}
	// JSON roundtrip transfers ownership of maps/slices away from the caller.
	payload, e := json.Marshal(b)
	if e != nil {
		return e
	}
	if len(payload) > 1<<20 {
		return errors.New("transaction exceeds 1 MiB")
	}
	var copyBatch model.Batch
	if e = json.Unmarshal(payload, &copyBatch); e != nil {
		return e
	}
	b = copyBatch
	// A heartbeat that would change nothing, such as one sent again, is
	// not written: it would cost a journal write and an fsync, under the
	// lock every observer's ingestion waits on, for nothing apply keeps.
	if b.Heartbeat != nil && !s.advances(*b.Heartbeat, b.Project, b.Revision) {
		b.Heartbeat = nil
	}
	pending := map[string]string{}
	now := s.options.Now().UTC()
	kept := make([]model.Observation, 0, len(b.Observations))
	for _, v := range b.Observations {
		if v.ID == "" || v.Check == "" || v.SampledAt.IsZero() {
			return errors.New("observation identity and timestamp required")
		}
		h := observationHash(v)
		if old, ok := s.seen[v.ID]; ok {
			if old.Hash != h {
				return ErrConflict
			}
			continue
		}
		if old, ok := pending[v.ID]; ok {
			if old != h {
				return ErrConflict
			}
			continue
		}
		if old, retained := s.rowHashes[v.ID]; retained {
			if old != h {
				return ErrConflict
			}
			continue
		}
		if s.expired(v.ID) {
			// An expired receipt's sample is past the ingestion window, so
			// this is its ID reused for something else.
			return ErrConflict
		}
		pending[v.ID] = h
		v.ReceivedAt = now
		kept = append(kept, v)
	}
	s.prune()
	b.Observations = kept
	if b.Heartbeat != nil {
		b.Heartbeat.ReceivedAt = now
	}
	// Bound bytes as well as row counts. Otherwise valid large facts can
	// create a checkpoint larger than the restart decoder can ever read.
	projected := s.stateBytes
	for _, v := range kept {
		raw, _ := json.Marshal(v)
		projected += int64(len(raw)) + 1024
	}
	if b.Heartbeat != nil {
		raw, _ := json.Marshal(b.Heartbeat)
		projected += int64(len(raw)) + 1024
		if old, ok := s.beats[b.Heartbeat.Observer]; ok {
			raw, _ := json.Marshal(old)
			projected -= int64(len(raw)) + 1024
		}
	}
	if err := s.checkScopeCapacity(b); err != nil {
		return err
	}
	if len(s.seen)+len(pending) > s.options.MaxReceipts || projected > s.options.MaxStateBytes {
		return ErrCapacity
	}
	if len(kept) == 0 && b.Heartbeat == nil {
		return nil
	}
	record, e := wrap(b)
	if e != nil {
		return e
	}
	record = append(record, '\n')
	n, e := s.file.Write(record)
	if e == nil && n != len(record) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = s.file.Sync()
	}
	if e != nil {
		s.poisoned = fmt.Errorf("journal durability failure; restart required: %w", e)
		return s.poisoned
	}
	s.bytes += int64(n)
	if e = s.apply(b, false); e != nil {
		s.poisoned = e
		return e
	}
	s.prune()
	if s.bytes >= s.options.CheckpointBytes {
		if e = s.checkpoint(); e != nil {
			s.poisoned = e
			return e
		}
	}
	return nil
}
func (s *Store) prune() {
	cutoff := s.options.Now().Add(-s.options.Retention)
	rows := s.rows[:0]
	for _, v := range s.rows {
		if !v.SampledAt.Before(cutoff) {
			rows = append(rows, v)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return before(rows[i], rows[j]) })
	if len(s.options.Observers) > 0 {
		counts := map[string]int{}
		allowed := map[string]bool{}
		for _, id := range s.options.Observers {
			allowed[id] = true
		}
		quota := s.options.MaxHistory / len(s.options.Observers)
		kept := make([]model.Observation, 0, min(len(rows), s.options.MaxHistory))
		for i := len(rows) - 1; i >= 0; i-- {
			if allowed[rows[i].Observer] && counts[rows[i].Observer] < quota {
				kept = append(kept, rows[i])
				counts[rows[i].Observer]++
			}
		}
		for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
			kept[i], kept[j] = kept[j], kept[i]
		}
		rows = kept
	} else if len(rows) > s.options.MaxHistory {
		rows = rows[len(rows)-s.options.MaxHistory:]
	}
	// Rows nothing holds any more give up their accounting, looked for only
	// when the history or a tail let some go.
	if len(rows) < len(s.rows) || s.rowsDropped {
		keep := make(map[string]bool, len(s.rowSizes))
		for _, v := range rows {
			keep[v.ID] = true
		}
		for _, tail := range s.tails {
			for _, v := range tail {
				keep[v.ID] = true
			}
		}
		for id, size := range s.rowSizes {
			if !keep[id] {
				s.rowBytes -= size
				s.rowScope[s.rowObservers[id]] -= size
				if s.rowScope[s.rowObservers[id]] == 0 {
					delete(s.rowScope, s.rowObservers[id])
				}
				delete(s.rowSizes, id)
				delete(s.rowHashes, id)
				delete(s.rowObservers, id)
			}
		}
		s.rowsDropped = false
	}
	s.rows = rows
	// A receipt binds an ID to its content for as long as a sample with
	// that ID could still be accepted: the ingestion window and an hour.
	// Then it goes, and its boot's floor keeps the ID bound for good, so
	// ageing out never reopens an observation identity. Receipts are taken
	// in order, so those due to go are at the front of the queue; one taken
	// while the clock stood behind waits for those in front of it.
	expiry := s.options.Now().Add(-s.options.Retention - receiptGrace)
	for s.receiptHead < len(s.receiptQueue) {
		id := s.receiptQueue[s.receiptHead]
		r, ok := s.seen[id]
		if ok && !r.At.Before(expiry) {
			break
		}
		s.receiptHead++
		// One whose ID names no boot has no floor to bind it, so it stays.
		if boot, seq, parsed := bootOf(id); ok && parsed {
			s.floors[boot] = max(s.floors[boot], seq)
			delete(s.seen, id)
			if s.scopeReceipts[r.Observer]--; s.scopeReceipts[r.Observer] == 0 {
				delete(s.scopeReceipts, r.Observer)
			}
		}
	}
	if s.receiptHead > 1024 && s.receiptHead > len(s.receiptQueue)/2 {
		s.receiptQueue = append([]string(nil), s.receiptQueue[s.receiptHead:]...)
		s.receiptHead = 0
	}
	s.scopeBytes = make(map[string]int64, len(s.scopeReceipts)+len(s.rowScope))
	for id, n := range s.scopeReceipts {
		s.scopeBytes[id] += int64(n) * 512
	}
	for boot := range s.floors {
		s.scopeBytes[receiptObserver(boot+".0")] += floorBytes
	}
	for id, size := range s.rowScope {
		s.scopeBytes[id] += size
	}
	s.stateBytes = 1024 + int64(len(s.seen))*512 + int64(len(s.floors))*floorBytes + int64(len(s.carries))*carryBytes + s.rowBytes
	for _, h := range s.beats {
		raw, _ := json.Marshal(h)
		s.stateBytes += int64(len(raw)) + 1024
	}
	if now := s.options.Now(); now.Sub(s.settled) >= time.Minute || now.Before(s.settled) {
		s.rollups.Settle(now)
		s.settled = now
	}
}
func (s *Store) allRows() []model.Observation {
	m := map[string]model.Observation{}
	for _, v := range s.rows {
		m[v.ID] = v
	}
	for _, tail := range s.tails {
		for _, v := range tail {
			m[v.ID] = v
		}
	}
	out := make([]model.Observation, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return before(out[i], out[j]) })
	return out
}
func (s *Store) checkpoint() error {
	s.settled = s.options.Now()
	s.rollups.Settle(s.settled)
	state := diskState{Schema: 1, Rows: s.allRows(), Seen: s.seen, Beats: s.beats, Floors: s.floors, Rollups: s.rollups, Carries: s.carries}
	b, e := wrap(state)
	if e != nil {
		return e
	}
	if len(b) > 256<<20 {
		return ErrCapacity
	}
	if e = secure.AtomicWrite(filepath.Join(s.dir, "checkpoint.json"), b, 0600); e != nil {
		return e
	}
	// If a crash occurs before truncation, replay is safe because the checkpoint
	// already contains the deduplication receipts and monotonic heartbeats.
	if e = s.file.Truncate(0); e != nil {
		return e
	}
	if _, e = s.file.Seek(0, io.SeekStart); e != nil {
		return e
	}
	if e = s.file.Sync(); e != nil {
		return e
	}
	s.bytes = 0
	return nil
}
func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store closed")
	}
	if s.poisoned != nil {
		return s.poisoned
	}
	e := s.checkpoint()
	if e != nil {
		s.poisoned = e
	}
	return e
}

// View returns detached copies so readers cannot mutate persisted evidence.
func (s *Store) View() ([]model.Observation, map[string]model.Heartbeat) {
	s.mu.Lock()
	rows := s.allRows()
	beats := map[string]model.Heartbeat{}
	for k, v := range s.beats {
		beats[k] = v
	}
	s.mu.Unlock()
	b, _ := json.Marshal(rows)
	var out []model.Observation
	_ = json.Unmarshal(b, &out)
	return out, beats
}

// History returns at most 1 MiB of newest full observations. A response may
// contain fewer than limit entries; serialization never holds the writer lock.
func (s *Store) History(check string, contracts map[string]string, revision string, limit int) []model.Observation {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	s.mu.Lock()
	cut := s.options.Now().Add(-s.options.Retention)
	out := []model.Observation{}
	size := int64(2)
	for i := len(s.rows) - 1; i >= 0 && len(out) < limit; i-- {
		v := s.rows[i]
		if v.Check == check && config.Counted(v, contracts, revision) && !v.SampledAt.Before(cut) {
			size += s.rowSizes[v.ID]
			if size > 1<<20 {
				break
			}
			out = append(out, v)
		}
	}
	s.mu.Unlock()
	b, _ := json.Marshal(out)
	_ = json.Unmarshal(b, &out)
	return out
}

// Availability is how check fared under contract over each of
// rollup.Windows up to now, or nil when its rollups hold nothing. It does
// not wait for the writer.
func (s *Store) Availability(check, contract string, now time.Time) []model.Availability {
	return s.rollups.Availability(contract+"/"+check, now)
}

// Rollups lays out check's rollups under contract from from until now in
// slots step wide (see rollup.Store.Buckets), with their total and the
// earliest moment they hold.
func (s *Store) Rollups(check, contract string, from time.Time, step time.Duration, now time.Time) (model.Rollup, []model.Rollup, time.Time) {
	key := contract + "/" + check
	total, _ := s.rollups.Window(key, from, now, now)
	return total, s.rollups.Buckets(key, from, now, step, now), s.rollups.Since(key)
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	e := s.file.Close()
	ue := s.unlock()
	if e != nil {
		return e
	}
	return ue
}
