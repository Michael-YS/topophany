package outbox

import (
	"container/heap"
	"strings"
	"time"
)

// One bounded index per recipient; each pending relationship owns one entry.
// Arbitrary acknowledgements remove entries in logarithmic time.
type pendingIndex struct {
	entries   []pendingEntry
	positions map[string]int
}

type pendingEntry struct {
	id string
	at time.Time
}

func (p *pendingIndex) Len() int { return len(p.entries) }
func (p *pendingIndex) Less(i, j int) bool {
	if c := p.entries[i].at.Compare(p.entries[j].at); c != 0 {
		return c < 0
	}
	return strings.Compare(p.entries[i].id, p.entries[j].id) < 0
}
func (p *pendingIndex) Swap(i, j int) {
	p.entries[i], p.entries[j] = p.entries[j], p.entries[i]
	p.positions[p.entries[i].id], p.positions[p.entries[j].id] = i, j
}
func (p *pendingIndex) Push(v any) {
	e := v.(pendingEntry)
	p.positions[e.id] = len(p.entries)
	p.entries = append(p.entries, e)
}
func (p *pendingIndex) Pop() any {
	i := len(p.entries) - 1
	e := p.entries[i]
	p.entries[i] = pendingEntry{}
	p.entries = p.entries[:i]
	delete(p.positions, e.id)
	return e
}

func (q *Queue) indexRecipient(recipient string, v Item, add bool) {
	p := q.pending[recipient]
	if add {
		if p == nil {
			p = &pendingIndex{positions: map[string]int{}}
			q.pending[recipient] = p
		}
		heap.Push(p, pendingEntry{v.ID, v.Batch.Observations[0].SampledAt})
	} else if p != nil {
		if i, ok := p.positions[v.ID]; ok {
			heap.Remove(p, i)
		}
		if p.Len() == 0 {
			delete(q.pending, recipient)
		}
	}
}
