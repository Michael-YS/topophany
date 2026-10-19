// Package rollup keeps what a collector's samples said, summed per check
// over time, so its history outlives the raw journal. A series is a check's
// samples under one contract. Each sample's outcome stands from when it was
// taken until the next sample or until it goes stale, whichever comes
// first, as freshness does in a snapshot; time no current sample stood for
// is unmeasured, never up or down. Sums are kept in buckets five minutes
// wide for two days, an hour wide for 32 days and a day wide after that,
// aligned to UTC, under a hard cap on their encoded size.
package rollup

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
)

// Bucket widths, and how long buckets stay five minutes or an hour wide
// before they are merged into wider ones. Widths nest: a day holds whole
// hours, and an hour whole five minutes.
const (
	FineWidth = 5 * time.Minute
	HourWidth = time.Hour
	DayWidth  = 24 * time.Hour
	FineFor   = 48 * time.Hour
	HourlyFor = 32 * 24 * time.Hour
)

// Bounds on a store's cap, in encoded bytes.
const (
	MinLimit = 64 << 10
	MaxLimit = 64 << 20
)

const (
	fine      = int64(FineWidth / time.Millisecond)
	hour      = int64(HourWidth / time.Millisecond)
	day       = int64(DayWidth / time.Millisecond)
	fineFor   = int64(FineFor / time.Millisecond)
	hourlyFor = int64(HourlyFor / time.Millisecond)
)

// Classes of time, by the outcome that stands for it, as a strip draws it.
const (
	up = iota
	degraded
	down
	unknown
	classes
)

// outcomes are what samples say, in the order they are counted; any other
// outcome counts as unknown.
var outcomes = [...]string{"ok", "policy", "fail", "error", "unknown"}

func classOf(outcome string) int {
	switch outcome {
	case "ok":
		return up
	case "policy":
		return degraded
	case "fail":
		return down
	}
	return unknown
}

func outcomeOf(outcome string) int {
	for i, o := range outcomes {
		if o == outcome {
			return i
		}
	}
	return len(outcomes) - 1
}

// defaultStale is how long a sample stays current when it is not said.
const defaultStale = 3 * 60 * 1000

// A bucket sums a series over [start, start+width), in Unix milliseconds;
// start is a multiple of width. ms is the time each class stood, n the
// samples taken by outcome, lat what the passing ones took.
type bucket struct {
	start, width int64
	ms           [classes]int64
	n            [len(outcomes)]int64
	lat          histogram
	unreconciled int64
}

func (b *bucket) end() int64 { return b.start + b.width }

func (b *bucket) merge(o bucket) {
	b.unreconciled += o.unreconciled
	for c := range b.ms {
		b.ms[c] += o.ms[c]
	}
	for i := range b.n {
		b.n[i] += o.n[i]
	}
	b.lat.merge(o.lat)
}

// size bounds a bucket's encoded size, in bytes.
func (b *bucket) size() int64 { return 192 + 12*int64(len(b.lat.bins)) }

// A series is a check's samples under one contract, all taken by owner,
// the check's observer. first is the earliest moment it holds, updated
// when a sample was last added by the collector's clock, open its newest
// sample, whose time still runs, and size what it costs, its buckets
// included.
type series struct {
	owner          string
	first, updated int64
	size           int64
	open           *open
	buckets        []bucket // ascending, never overlapping
	recent         []open   // ordered sample boundaries still available for correction
	recentCapacity int      // full backing allocation, including discarded prefixes
	correctionFrom int64    // older boundaries were compacted or predate an upgrade
}

type open struct {
	at, stale int64
	class     int
}

// seriesSize bounds what a series costs besides its buckets.
func seriesSize(key, owner string) int64 {
	// Include escaped keys/owners, the open sample and the JSON envelope.
	k, _ := json.Marshal(key)
	o, _ := json.Marshal(owner)
	return 256 + int64(len(k)+len(o))
}

const diskBytes = 32

// Store holds the series of a collector. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	limit   int64
	series  map[string]*series
	size    int64
	current func(key string) bool
	reorder int64 // reconciliation window, in milliseconds
}

// New is an empty store that keeps its series within limit encoded bytes.
func New(limit int64) *Store {
	return &Store{limit: limit, size: diskBytes, series: map[string]*series{}, reorder: int64((24*time.Hour + 30*time.Second) / time.Millisecond)}
}

// SetCurrent says which series are still counted. A store that must shed
// drops the others first, those least recently added to first, before the
// oldest buckets of any series.
func (r *Store) SetCurrent(current func(key string) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = current
}

// Add folds in a sample of the series key, taken by the observer owner:
// taken at at, saying outcome, taking elapsed milliseconds, current for
// stale. The newest sample before
// it stands until at or until it went stale, whichever came first. A
// late sample corrects the intervals around it while their boundaries
// are retained. Outside that window it counts as unreconciled. now, the
// collector's clock, decides how wide a new bucket is.
func (r *Store) Add(key, owner string, at time.Time, outcome string, elapsed int64, stale time.Duration, now time.Time) {
	t, n := at.UnixMilli(), now.UnixMilli()
	if t <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.series[key]
	if s == nil {
		s = &series{owner: owner}
		r.series[key] = s
		r.grow(s, seriesSize(key, owner))
	}
	if s.owner != owner {
		r.grow(s, seriesSize(key, owner)-seriesSize(key, s.owner))
	}
	s.owner = owner
	s.updated = n
	if s.first == 0 || t < s.first {
		s.first = t
	}
	st := stale.Milliseconds()
	if st <= 0 {
		st = defaultStale
	}
	c := classOf(outcome)
	r.discardBoundaries(s, n-r.reorder)
	reconciled := r.insert(s, open{t, st, c}, n)
	i := r.bucket(s, t, n)
	b := &s.buckets[i]
	before := b.size()
	b.n[outcomeOf(outcome)]++
	if !reconciled {
		b.unreconciled++
	}
	if outcome == "ok" {
		b.lat.add(elapsed)
	}
	r.grow(s, b.size()-before)
	r.shed()
}

// grow counts n more bytes to s and the store.
func (r *Store) grow(s *series, n int64) {
	s.size += n
	r.size += n
}

// spend credits [from, to) to class.
func (r *Store) spend(s *series, from, to int64, class int, now int64) {
	for from < to {
		b := &s.buckets[r.bucket(s, from, now)]
		end := min(to, b.end())
		b.ms[class] += end - from
		from = end
	}
}

// bucket is the index of the bucket holding t, made as wide as t's age at
// now asks for if there is none. Narrower buckets inside a new one, as a
// clock set back can leave, join it.
func (r *Store) bucket(s *series, t, now int64) int {
	i := sort.Search(len(s.buckets), func(i int) bool { return s.buckets[i].end() > t })
	if i < len(s.buckets) && s.buckets[i].start <= t {
		return i
	}
	w := widthAt(t, now)
	nb := bucket{start: floor(t, w), width: w}
	k, j := i, i
	for k > 0 && s.buckets[k-1].start >= nb.start {
		k--
	}
	for j < len(s.buckets) && s.buckets[j].start < nb.end() {
		j++
	}
	for _, old := range s.buckets[k:j] {
		nb.merge(old)
		r.grow(s, -old.size())
	}
	s.buckets = slices.Replace(s.buckets, k, j, nb)
	r.grow(s, nb.size())
	return k
}

// widthAt is how wide a bucket holding t is at now: a day once that whole
// day is HourlyFor old, an hour once that whole hour is FineFor old, and
// five minutes before that.
func widthAt(t, now int64) int64 {
	switch {
	case floor(t, day)+day <= now-hourlyFor:
		return day
	case floor(t, hour)+hour <= now-fineFor:
		return hour
	}
	return fine
}

func floor(t, w int64) int64 { return t - ((t%w)+w)%w }

// Settle merges buckets old enough into wider ones and sheds what the cap
// cannot hold. A collector settles its rollups every minute and before it
// writes them.
func (r *Store) Settle(now time.Time) {
	n := now.UnixMilli()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.series {
		r.discardBoundaries(s, n-r.reorder)
	}
	r.size = diskBytes
	for key, s := range r.series {
		s.buckets = compact(s.buckets, n)
		s.size = seriesSize(key, s.owner) + int64(len(s.recent))*boundaryBytes
		for i := range s.buckets {
			s.size += s.buckets[i].size()
		}
		r.size += s.size
	}
	r.shed()
}

// compact merges each bucket into the wider one its age at now asks for.
func compact(bs []bucket, now int64) []bucket {
	out := bs[:0]
	for _, b := range bs {
		if w := widthAt(b.start, now); w > b.width {
			b.start, b.width = floor(b.start, w), w
		}
		if k := len(out) - 1; k >= 0 && b.start < out[k].end() {
			if b.width > out[k].width {
				out[k].start, out[k].width = b.start, b.width
			}
			out[k].merge(b)
			continue
		}
		out = append(out, b)
	}
	clear(bs[len(out):])
	return trimCapacity(out)
}

// Eviction must release backing allocations as well as logical entries. Keep
// at most a constant factor of spare capacity, amortizing copies over removals.
func trimCapacity[T any](items []T) []T {
	if len(items) == 0 {
		return nil
	}
	if cap(items) > 2*len(items) {
		return slices.Clone(items)
	}
	return items
}

// shed drops what the cap cannot hold: series no longer counted, those
// least recently added to first, then the oldest bucket of the observer
// whose series cost the most. An observer loses rollups only while none
// holds more, so one that sends all it may cannot shorten the history of
// another that holds no more than its share of the cap.
func (r *Store) shed() {
	for r.size > r.limit {
		key, oldest := "", int64(math.MaxInt64)
		if r.current != nil {
			for k, s := range r.series {
				if !r.current(k) && (s.updated < oldest || s.updated == oldest && k < key) {
					key, oldest = k, s.updated
				}
			}
		}
		if key != "" {
			r.drop(key)
			continue
		}
		cost := map[string]int64{}
		for _, s := range r.series {
			cost[s.owner] += s.size
		}
		owner, most := "", int64(-1)
		for o, n := range cost {
			if n > most || n == most && o < owner {
				owner, most = o, n
			}
		}
		for k, s := range r.series {
			if s.owner == owner && len(s.buckets) > 0 && (s.buckets[0].start < oldest || s.buckets[0].start == oldest && k < key) {
				key, oldest = k, s.buckets[0].start
			}
		}
		if key == "" {
			// With no bucket left, the owner's series metadata must give
			// way too. Otherwise a large active topology defeats the cap.
			for k, s := range r.series {
				if s.owner == owner && (s.updated < oldest || s.updated == oldest && k < key) {
					key, oldest = k, s.updated
				}
			}
			if key == "" {
				return
			} // only the fixed envelope remains
			r.drop(key)
			continue
		}
		s := r.series[key]
		end := s.buckets[0].end()
		r.grow(s, -s.buckets[0].size())
		s.buckets = trimCapacity(slices.Delete(s.buckets, 0, 1))
		r.discardBoundaries(s, end)
		switch {
		case len(s.buckets) > 0:
			s.first = max(s.first, s.buckets[0].start)
		case s.open != nil:
			s.first = max(s.first, s.open.at)
		default:
			r.drop(key)
		}
	}
}

func (r *Store) drop(key string) {
	r.size -= r.series[key].size
	delete(r.series, key)
}

// Size is the store's encoded size, as bounded when it last settled and
// counted since.
func (r *Store) Size() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// Keys are the series the store holds, in order.
func (r *Store) Keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.series))
	for k := range r.series {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
