// Package outbox stores immutable observations until every configured collector
// has acknowledged them. Permanent rejections are quarantined, never erased.
package outbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var ErrFull = errors.New("outbox byte budget exhausted; execution must stop")

type Item struct {
	Schema  int         `json:"schema"`
	ID      string      `json:"id"`
	Batch   model.Batch `json:"batch"`
	Pending []string    `json:"pending"`
	SHA256  string      `json:"sha256"`
}
type Queue struct {
	mu      sync.Mutex
	dir     string
	project string
	max     int64
	used    int64
	items   map[string]Item
	unlock  func() error
	closed  bool
}

func itemHash(v Item) string { v.SHA256 = ""; b, _ := json.Marshal(v); return secure.Hash(string(b)) }
func Open(dir, project string, maxBytes int64) (*Queue, error) {
	if maxBytes < 1024 {
		return nil, errors.New("outbox budget must be at least 1024 bytes")
	}
	if e := os.MkdirAll(filepath.Join(dir, "pending"), 0700); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(filepath.Join(dir, "quarantine"), 0700); e != nil {
		return nil, e
	}
	unlock, e := secure.Lock(filepath.Join(dir, "writer.lock"))
	if e != nil {
		return nil, e
	}
	q := &Queue{dir: dir, project: project, max: maxBytes, items: map[string]Item{}, unlock: unlock}
	ok := false
	defer func() {
		if !ok {
			unlock()
		}
	}()
	for _, sub := range []string{"pending", "quarantine"} {
		entries, e := os.ReadDir(filepath.Join(dir, sub))
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
			info, e := entry.Info()
			if e != nil {
				return nil, e
			}
			q.used += info.Size()
			if q.used > maxBytes {
				return nil, ErrFull
			}
			if info.Size() > 2<<20 {
				return nil, errors.New("oversized outbox record")
			}
			if sub == "quarantine" {
				continue
			}
			b, e := secure.ReadBounded(filepath.Join(dir, sub, entry.Name()), 2<<20)
			if e != nil {
				return nil, e
			}
			var v Item
			if e = json.Unmarshal(b, &v); e != nil || v.Schema != 1 || v.SHA256 != itemHash(v) || entry.Name() != v.ID+".json" || v.Batch.Project != project || !validItem(v) {
				return nil, errors.New("outbox corruption or project mismatch")
			}
			q.items[v.ID] = v
		}
	}
	if q.used > maxBytes {
		return nil, ErrFull
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
	if st, e := os.Stat(q.file(v.ID)); e == nil {
		old = st.Size()
	}
	if q.used-old+int64(len(b)) > q.max {
		return ErrFull
	}
	if e = secure.AtomicWrite(q.file(v.ID), b, 0600); e != nil {
		return e
	}
	q.used = q.used - old + int64(len(b))
	q.items[v.ID] = v
	return nil
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
	var owned model.Batch
	_ = json.Unmarshal(raw, &owned)
	v := Item{Schema: 1, ID: id, Batch: owned, Pending: append([]string(nil), recipients...)}
	sort.Strings(v.Pending)
	for i := 1; i < len(v.Pending); i++ {
		if v.Pending[i] == v.Pending[i-1] {
			return errors.New("duplicate recipient")
		}
	}
	return q.write(v)
}
func (q *Queue) Pending(recipient string, limit int) []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []Item{}
	for _, v := range q.items {
		for _, r := range v.Pending {
			if r == recipient {
				out = append(out, v)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i].Batch.Observations[0]
		b := out[j].Batch.Observations[0]
		if a.SampledAt.Equal(b.SampledAt) {
			return out[i].ID < out[j].ID
		}
		return a.SampledAt.Before(b.SampledAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	b, _ := json.Marshal(out)
	var detached []Item
	_ = json.Unmarshal(b, &detached)
	return detached
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
		st, e := os.Stat(q.file(id))
		if e != nil {
			return e
		}
		if e = os.Remove(q.file(id)); e != nil {
			return e
		}
		q.used -= st.Size()
		delete(q.items, id)
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
		if st, e := os.Stat(file); e == nil {
			old = st.Size()
		}
		if q.used-old+int64(len(b)) > q.max {
			return ErrFull
		}
		if e = secure.AtomicWrite(file, b, 0600); e != nil {
			return e
		}
		q.used = q.used - old + int64(len(b))
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
