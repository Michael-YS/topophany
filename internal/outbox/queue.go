// Package outbox stores immutable observations until every configured collector
// has acknowledged them. Permanent rejections enter a separate, rotating forensic budget.
package outbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var ErrFull = errors.New("outbox byte budget exhausted; execution must stop")

// DegradedError reports destinations whose new evidence could not be queued.
// Other destinations have already been durably queued; callers must continue
// delivery. Add returns ErrFull, never this, when no destination had room.
type DegradedError struct{ Recipients []string }

func (e *DegradedError) Error() string {
	return fmt.Sprintf("outbox destination quota exhausted; new evidence dropped for %v", e.Recipients)
}

type Item struct {
	Schema  int         `json:"schema"`
	ID      string      `json:"id"`
	Batch   model.Batch `json:"batch"`
	Pending []string    `json:"pending"`
	SHA256  string      `json:"sha256"`
}
type Queue struct {
	mu             sync.Mutex
	dir            string
	project        string
	max            int64
	used           int64
	quarantineUsed int64
	items          map[string]Item
	itemSizes      map[string]int64
	recipientBytes map[string]int64
	unlock         func() error
	closed         bool
}

func itemHash(v Item) string { v.SHA256 = ""; b, _ := json.Marshal(v); return secure.Hash(string(b)) }
func Open(dir, project string, maxBytes int64) (*Queue, error) {
	if maxBytes < 1024 {
		return nil, errors.New("outbox budget must be at least 1024 bytes")
	}
	// A record is queued once it and pending are synced, which makes
	// neither pending nor the directories above it durable: those are
	// synced as they are made.
	if e := secure.MkdirAll(filepath.Join(dir, "pending"), 0700); e != nil {
		return nil, e
	}
	if e := secure.MkdirAll(filepath.Join(dir, "quarantine"), 0700); e != nil {
		return nil, e
	}
	unlock, e := secure.Lock(filepath.Join(dir, "writer.lock"))
	if e != nil {
		return nil, e
	}
	q := &Queue{dir: dir, project: project, max: maxBytes, items: map[string]Item{}, itemSizes: map[string]int64{}, recipientBytes: map[string]int64{}, unlock: unlock}
	ok := false
	defer func() {
		if !ok {
			unlock()
		}
	}()
	for _, sub := range []string{"pending", "quarantine"} {
		entries, e := queueReadDir(filepath.Join(dir, sub))
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil, errors.New("invalid outbox entry")
			}
			info, e := queueInfo(entry)
			if e != nil {
				return nil, e
			}
			if !info.Mode().IsRegular() {
				return nil, errors.New("invalid outbox entry")
			}
			q.used += info.Size()
			if q.used-q.quarantineUsed > maxBytes && sub == "pending" {
				return nil, ErrFull
			}
			if info.Size() > 2<<20 {
				return nil, errors.New("oversized outbox record")
			}
			if sub == "quarantine" {
				q.quarantineUsed += info.Size()
				continue
			}
			b, e := queueRead(filepath.Join(dir, sub, entry.Name()), 2<<20)
			if e != nil {
				return nil, e
			}
			var v Item
			if e = json.Unmarshal(b, &v); e != nil || v.Schema != 1 || v.SHA256 != itemHash(v) || entry.Name() != v.ID+".json" || v.Batch.Project != project || !validItem(v) {
				return nil, errors.New("outbox corruption or project mismatch")
			}
			q.account(v, info.Size())
		}
	}
	if q.used-q.quarantineUsed > maxBytes {
		return nil, ErrFull
	}
	if e := q.trimQuarantine("", "", 0); e != nil {
		return nil, e
	}
	ok = true
	return q, nil
}
func validItem(v Item) bool {
	if len(v.Batch.Observations) == 0 || len(v.Batch.Observations) > 100 || len(v.Pending) == 0 || len(v.Pending) > 8 {
		return false
	}
	raw, e := json.Marshal(v.Batch)
	if e != nil || v.ID != secure.Hash(string(raw)) {
		return false
	}
	seen := map[string]bool{}
	for _, r := range v.Pending {
		if len(r) != 16 || strings.Trim(r, "0123456789abcdef") != "" || seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
}
func (q *Queue) file(id string) string { return filepath.Join(q.dir, "pending", id+".json") }
func (q *Queue) write(v Item) error {
	v.SHA256 = itemHash(v)
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > 1<<20 {
		return errors.New("outbox record exceeds 1 MiB")
	}
	old := int64(0)
	if st, e := queueStat(q.file(v.ID)); e == nil {
		old = st.Size()
	}
	if q.used-q.quarantineUsed-old+int64(len(b)) > q.max {
		return ErrFull
	}
	if e = queueWrite(q.file(v.ID), b, 0600); e != nil {
		return e
	}
	q.used = q.used - old + int64(len(b))
	q.account(v, int64(len(b)))
	return nil
}

// account updates independent recipient reservations on durable record changes.
func (q *Queue) account(v Item, size int64) {
	old := q.items[v.ID]
	for _, r := range old.Pending {
		q.recipientBytes[r] -= q.itemSizes[v.ID]
	}
	for _, r := range v.Pending {
		q.recipientBytes[r] += size
	}
	q.items[v.ID], q.itemSizes[v.ID] = v, size
}

func (q *Queue) Add(b model.Batch, recipients []string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("outbox closed")
	}
	if b.Project != q.project || len(b.Observations) == 0 || len(b.Observations) > 100 || len(recipients) == 0 || len(recipients) > 8 {
		return errors.New("outbox needs matching project, observations and recipients")
	}
	for _, r := range recipients {
		if len(r) != 16 || strings.Trim(r, "0123456789abcdef") != "" {
			return errors.New("invalid recipient ID")
		}
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return e
	}
	if len(raw) > (1<<20)-8192 {
		return errors.New("outbox batch exceeds transport budget")
	}
	id := secure.Hash(string(raw))
	if _, ok := q.items[id]; ok {
		return nil
	}
	v := Item{Schema: 1, ID: id, Batch: model.CloneBatch(b), Pending: append([]string(nil), recipients...)}
	sort.Strings(v.Pending)
	for i := 1; i < len(v.Pending); i++ {
		if v.Pending[i] == v.Pending[i-1] {
			return errors.New("duplicate recipient")
		}
	}
	// Charge each recipient the full record size, even though bytes are shared.
	// This conservative reservation guarantees the sum cannot exceed the global
	// budget and is reconstructed from pending records after every restart.
	v.SHA256 = itemHash(v)
	encoded, _ := json.Marshal(v)
	share := q.max / int64(len(recipients))
	admitted, rejected := []string{}, []string{}
	for _, r := range v.Pending {
		if q.recipientBytes[r]+int64(len(encoded)) > share {
			rejected = append(rejected, r)
		} else {
			admitted = append(admitted, r)
		}
	}
	// With room at no recipient the record is kept for none: that is a
	// full outbox, however many recipients there are, not a degraded one.
	if len(admitted) == 0 {
		return ErrFull
	}
	v.Pending = admitted
	if err := q.write(v); err != nil {
		return err
	}
	if len(rejected) > 0 {
		return &DegradedError{Recipients: rejected}
	}
	return nil
}

// Pending is the earliest sampled items still owed to recipient, at most
// limit of them (all when limit is not positive). It keeps the earliest as
// it finds them rather than sorting the whole backlog, which a long outage
// can make tens of thousands of items, every round.
func (q *Queue) Pending(recipient string, limit int) []Item {
	return q.PendingBytes(recipient, limit, 0)
}

// PendingBytes bounds a delivery's detached copy by encoded record bytes as
// well as count. Always include the first item so a large record can progress.
// Zero maxBytes preserves Pending's unrestricted diagnostic behavior.
func (q *Queue) PendingBytes(recipient string, limit int, maxBytes int64) []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []Item{}
	for _, v := range q.items {
		switch {
		case !slices.Contains(v.Pending, recipient):
		case limit <= 0:
			out = append(out, v)
		case len(out) < limit || pendingOrder(v, out[limit-1]) < 0:
			i, _ := slices.BinarySearchFunc(out, v, pendingOrder)
			out = slices.Insert(out, i, v)
			out = out[:min(len(out), limit)]
		}
	}
	if limit <= 0 {
		slices.SortFunc(out, pendingOrder)
	}
	if maxBytes > 0 {
		var size int64
		for i, item := range out {
			n := q.itemSizes[item.ID]
			if i > 0 && size+n > maxBytes {
				out = out[:i]
				break
			}
			size += n
		}
	}
	for i := range out {
		out[i].Batch = model.CloneBatch(out[i].Batch)
		out[i].Pending = slices.Clone(out[i].Pending)
	}
	return out
}

// pendingOrder orders items by when their first sample was taken, then by ID.
func pendingOrder(a, b Item) int {
	if c := a.Batch.Observations[0].SampledAt.Compare(b.Batch.Observations[0].SampledAt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}
func (q *Queue) Ack(ids []string, recipient string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("outbox closed")
	}
	for _, id := range ids {
		if e := q.ack(id, recipient); e != nil {
			return e
		}
	}
	return nil
}
func (q *Queue) ack(id, recipient string) error {
	v, ok := q.items[id]
	if !ok {
		return nil
	}
	pending := []string{}
	found := false
	for _, r := range v.Pending {
		if r == recipient {
			found = true
		} else {
			pending = append(pending, r)
		}
	}
	if !found {
		return nil
	}
	v.Pending = pending
	if len(pending) == 0 {
		st, e := queueStat(q.file(id))
		if e != nil {
			return e
		}
		if e = queueRemove(q.file(id)); e != nil {
			return e
		}
		q.used -= st.Size()
		q.account(Item{ID: id}, 0)
		delete(q.items, id)
		delete(q.itemSizes, id)
		return secure.SyncDir(filepath.Join(q.dir, "pending"))
	}
	return q.write(v)
}
func (q *Queue) Quarantine(ids []string, recipient, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("outbox closed")
	}
	if len(recipient) != 16 || strings.Trim(recipient, "0123456789abcdef") != "" {
		return errors.New("invalid quarantine recipient")
	}
	reason = secure.Text(reason)
	if len(reason) > 256 {
		reason = reason[:256]
	}
	for _, id := range ids {
		v, ok := q.items[id]
		if !ok {
			continue
		}
		file := filepath.Join(q.dir, "quarantine", id+"-"+recipient+".json")
		b, e := json.Marshal(struct {
			Item      Item   `json:"item"`
			Recipient string `json:"recipient"`
			Reason    string `json:"reason"`
		}{v, recipient, reason})
		if e != nil {
			return e
		}
		old := int64(0)
		if st, e := queueStat(file); e == nil {
			old = st.Size()
		}
		if e := q.trimQuarantine(recipient, filepath.Base(file), int64(len(b))-old); e != nil {
			return e
		}
		if e = queueWrite(file, b, 0600); e != nil {
			return e
		}
		q.used = q.used - old + int64(len(b))
		q.quarantineUsed += int64(len(b)) - old
		if e = q.ack(id, recipient); e != nil {
			return e
		}
	}
	return nil
}
func (q *Queue) Stats() (int, int64) { q.mu.Lock(); defer q.mu.Unlock(); return len(q.items), q.used }
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	return q.unlock()
}
func Recipient(url string) string { return secure.Hash(url)[:16] }
func (q *Queue) String() string {
	n, b := q.Stats()
	return fmt.Sprintf("outbox: %d pending records, %d bytes including quarantine", n, b)
}
