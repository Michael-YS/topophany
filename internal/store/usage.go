package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Usage is how much of its budgets a store holds and, for receipts, where
// they settle. A receipt lasts the ingestion window and an hour, so at a
// steady rate an observer holds what it delivers in that long: the last
// hour's receipts times the hours a receipt lasts.
type Usage struct {
	Receipts      int
	MaxReceipts   int
	StateBytes    int64
	MaxStateBytes int64
	Hours         float64 // how long a receipt lasts
	Observers     []ObserverUsage
}

// ObserverUsage is one observer's share of a store: all of it when the
// store is told of no observers, which Observer then leaves empty.
type ObserverUsage struct {
	Observer     string
	Receipts     int
	ReceiptLimit int
	Bytes        int64 // its receipts, rows and floors, or all state
	ByteLimit    int64
	LastHour     int // receipts taken in the last hour
	Settles      int // receipts held at the last hour's rate
	// FullIn is how long, at the last hour's rate, until its receipts fill
	// its share, of receipts or of bytes; Fills is false when they settle
	// within both.
	Fills  bool
	FullIn time.Duration
}

// Usage reports what the store holds against its budgets as of now.
func (s *Store) Usage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.options.Now()
	u := Usage{Receipts: len(s.seen), MaxReceipts: s.options.MaxReceipts, StateBytes: s.stateBytes, MaxStateBytes: s.options.MaxStateBytes, Hours: (s.options.Retention + receiptGrace).Hours()}
	// The queue is in the order receipts were taken: the last hour's are at
	// its end.
	recent := map[string]int{}
	total := 0
	for i := len(s.receiptQueue) - 1; i >= s.receiptHead; i-- {
		r, ok := s.seen[s.receiptQueue[i]]
		if !ok {
			continue
		}
		if r.At.Before(now.Add(-time.Hour)) {
			break
		}
		recent[r.Observer]++
		total++
	}
	// A share fills when its receipts settle beyond it, or beyond its bytes
	// once the rows and floors it holds are counted; it fills at the rate
	// receipts come in, whichever runs out first.
	share := func(o ObserverUsage, rate int) ObserverUsage {
		o.LastHour = rate
		o.Settles = int(float64(rate) * u.Hours)
		if rate == 0 {
			return o
		}
		fill := func(room, perHour float64) {
			in := time.Duration(max(room, 0) / perHour * float64(time.Hour))
			if !o.Fills || in < o.FullIn {
				o.Fills, o.FullIn = true, in
			}
		}
		if o.Settles > o.ReceiptLimit {
			fill(float64(o.ReceiptLimit-o.Receipts), float64(rate))
		}
		if held := o.Bytes - int64(o.Receipts)*512; int64(o.Settles)*512+held > o.ByteLimit {
			fill(float64(o.ByteLimit-o.Bytes), float64(rate)*512)
		}
		return o
	}
	n := len(s.options.Observers)
	if n == 0 {
		u.Observers = []ObserverUsage{share(ObserverUsage{Receipts: len(s.seen), ReceiptLimit: s.options.MaxReceipts, Bytes: s.stateBytes, ByteLimit: s.options.MaxStateBytes}, total)}
		return u
	}
	byteLimit := (s.options.MaxStateBytes - 1024 - int64(n)*2048) / int64(n)
	for _, id := range s.options.Observers {
		u.Observers = append(u.Observers, share(ObserverUsage{Observer: id, Receipts: s.scopeReceipts[id], ReceiptLimit: s.options.MaxReceipts / n, Bytes: s.scopeBytes[id], ByteLimit: byteLimit}, recent[id]))
	}
	sort.Slice(u.Observers, func(i, j int) bool { return u.Observers[i].Observer < u.Observers[j].Observer })
	return u
}

// String says in a line what u holds, and of each observer how far its
// share is used and where its receipts settle.
func (u Usage) String() string {
	parts := []string{fmt.Sprintf("storage: %d of %d receipts, %s of %s state", u.Receipts, u.MaxReceipts, mib(u.StateBytes), mib(u.MaxStateBytes))}
	for _, o := range u.Observers {
		name := o.Observer
		if name == "" {
			name = "all"
		}
		line := fmt.Sprintf("%s %d of %d receipts, %s of %s, settling at %d by the last hour's %d", name, o.Receipts, o.ReceiptLimit, mib(o.Bytes), mib(o.ByteLimit), o.Settles, o.LastHour)
		if o.Fills {
			line += fmt.Sprintf(", full in %s", o.FullIn.Round(time.Minute))
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "; ")
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }
