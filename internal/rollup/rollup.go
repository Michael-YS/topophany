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
	"errors"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
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
}

func (b *bucket) end() int64 { return b.start + b.width }

func (b *bucket) merge(o bucket) {
	for c := range b.ms {
		b.ms[c] += o.ms[c]
	}
	for i := range b.n {
		b.n[i] += o.n[i]
	}
	b.lat.merge(o.lat)
}

// size bounds a bucket's encoded size, in bytes.
func (b *bucket) size() int64 { return 160 + 12*int64(len(b.lat.bins)) }

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
}

type open struct {
	at, stale int64
	class     int
}

// seriesSize bounds what a series costs besides its buckets.
func seriesSize(key string) int64 { return 128 + int64(len(key)) }

// Store holds the series of a collector. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	limit   int64
	series  map[string]*series
	size    int64
	current func(key string) bool
}

// New is an empty store that keeps its series within limit encoded bytes.
func New(limit int64) *Store {
	return &Store{limit: limit, series: map[string]*series{}}
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
// sample no newer than the newest is counted, but the time it would stand
// for is already accounted for. now, the collector's clock, decides how
// wide a new bucket is.
func (r *Store) Add(key, owner string, at time.Time, outcome string, elapsed int64, stale time.Duration, now time.Time) {
	t, n := at.UnixMilli(), now.UnixMilli()
	if t <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.series[key]
	if s == nil {
		s = &series{}
		r.series[key] = s
		r.grow(s, seriesSize(key))
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
	switch {
	case s.open == nil:
		s.open = &open{t, st, c}
	case t > s.open.at:
		r.spend(s, s.open.at, min(t, s.open.at+s.open.stale), s.open.class, n)
		s.open = &open{t, st, c}
	}
	i := r.bucket(s, t, n)
	b := &s.buckets[i]
	before := b.size()
	b.n[outcomeOf(outcome)]++
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
	r.size = 0
	for key, s := range r.series {
		s.buckets = compact(s.buckets, n)
		s.size = seriesSize(key)
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
	return out
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
		cost, held := map[string]int64{}, map[string]bool{}
		for _, s := range r.series {
			cost[s.owner] += s.size
			held[s.owner] = held[s.owner] || len(s.buckets) > 0
		}
		owner, most := "", int64(-1)
		for o, n := range cost {
			if held[o] && (n > most || n == most && o < owner) {
				owner, most = o, n
			}
		}
		for k, s := range r.series {
			if s.owner == owner && len(s.buckets) > 0 && (s.buckets[0].start < oldest || s.buckets[0].start == oldest && k < key) {
				key, oldest = k, s.buckets[0].start
			}
		}
		if key == "" {
			return
		}
		s := r.series[key]
		r.grow(s, -s.buckets[0].size())
		s.buckets = slices.Delete(s.buckets, 0, 1)
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

// tally adds up buckets and the time of an open sample.
type tally struct {
	ms  [classes]float64
	n   [len(outcomes)]int64
	lat histogram
}

func (a *tally) count(b bucket) {
	for i := range a.n {
		a.n[i] += b.n[i]
	}
	a.lat.merge(b.lat)
}

// open adds the time the open sample o has stood within [from, to).
func (a *tally) open(o *open, from, to int64) {
	if o == nil {
		return
	}
	if lo, hi := max(o.at, from), min(o.at+o.stale, to); hi > lo {
		a.ms[o.class] += float64(hi - lo)
	}
}

// rollup is the tally over span milliseconds from start. Time is rounded
// to the second so that the classes never add up to more than the span.
func (a *tally) rollup(start, span int64) model.Rollup {
	secs := span / 1000
	out := model.Rollup{Start: time.UnixMilli(start).UTC(), Seconds: secs}
	total := 0.0
	for _, ms := range a.ms {
		total += ms
	}
	scale := 1.0
	if total > float64(span) {
		scale = float64(span) / total
	}
	cum, prev := 0.0, int64(0)
	values := [classes]*int64{&out.Up, &out.Degraded, &out.Down, &out.Unknown}
	for c, ms := range a.ms {
		cum += ms * scale
		v := min(secs, int64(math.Round(cum/1000)))
		*values[c] = v - prev
		prev = v
	}
	out.Unmeasured = secs - prev
	for i, n := range a.n {
		if n > 0 {
			if out.Outcomes == nil {
				out.Outcomes = map[string]int64{}
			}
			out.Outcomes[outcomes[i]] = n
			out.Samples += n
		}
	}
	if a.lat.n > 0 {
		out.Latency = &model.Latency{Samples: a.lat.n, P50: a.lat.quantile(.5), P95: a.lat.quantile(.95), Max: a.lat.max}
	}
	return out
}

// Window is the series key over [from, to), as read at now, and the
// earliest moment it holds when that is after from. A bucket the window
// begins inside counts the share of it that the window covers, as though
// its time were spread evenly; one the window ends inside counts whole, as
// its time ran before now, but never for more than the window holds of it.
// The samples of a bucket the window begins inside count when the window
// holds at least half of it.
func (r *Store) Window(key string, from, to, now time.Time) (model.Rollup, time.Time) {
	f, t := from.UnixMilli(), to.UnixMilli()
	r.mu.Lock()
	defer r.mu.Unlock()
	var a tally
	s := r.series[key]
	if s == nil {
		return a.rollup(f, t-f), time.Time{}
	}
	for i := sort.Search(len(s.buckets), func(i int) bool { return s.buckets[i].end() > f }); i < len(s.buckets) && s.buckets[i].start < t; i++ {
		b := s.buckets[i]
		lo, hi := max(b.start, f), min(b.end(), t)
		share := 1.0
		if b.start < f {
			share = float64(hi-lo) / float64(b.width)
		}
		total := 0.0
		for _, ms := range b.ms {
			total += float64(ms) * share
		}
		if total > float64(hi-lo) {
			share *= float64(hi-lo) / total
		}
		for c, ms := range b.ms {
			a.ms[c] += float64(ms) * share
		}
		if b.start >= f || 2*(hi-lo) >= b.width {
			a.count(b)
		}
	}
	a.open(s.open, f, min(t, now.UnixMilli()))
	since := time.Time{}
	if s.first > f {
		since = time.UnixMilli(s.first).UTC()
	}
	return a.rollup(f, t-f), since
}

// Windows are the spans a snapshot says how each check fared over.
var Windows = []struct {
	Name string
	Span time.Duration
}{{"24h", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour}, {"30d", 30 * 24 * time.Hour}}

// Availability is how the series key fared over each of Windows up to now,
// or nil when the store holds nothing of it.
func (r *Store) Availability(key string, now time.Time) []model.Availability {
	r.mu.Lock()
	_, held := r.series[key]
	r.mu.Unlock()
	if !held {
		return nil
	}
	out := make([]model.Availability, 0, len(Windows))
	for _, w := range Windows {
		got, since := r.Window(key, now.Add(-w.Span), now, now)
		out = append(out, model.Availability{Window: w.Name, Rollup: got, Since: since})
	}
	return out
}

// Steps are the widths Buckets lays slots out in, by name.
var Steps = map[string]time.Duration{"5m": FineWidth, "1h": HourWidth, "1d": DayWidth}

// MaxSlots bounds how many slots one layout has; MaxSpan how far back one
// reaches.
const (
	MaxSlots = 1000
	MaxSpan  = 800 * 24 * time.Hour
)

// DefaultSpan is how far back a layout in step reaches when not said: a
// day of five minutes, a week of hours, 90 days.
func DefaultSpan(step time.Duration) time.Duration {
	switch step {
	case FineWidth:
		return 24 * time.Hour
	case HourWidth:
		return 7 * 24 * time.Hour
	}
	return 90 * 24 * time.Hour
}

// ParseSpan reads a span such as "90m", "24h" or "30d": a whole number of
// minutes, hours or days.
func ParseSpan(s string) (time.Duration, error) {
	if len(s) < 2 || len(s) > 8 {
		return 0, errors.New("span must be a number of minutes, hours or days, such as 24h or 30d")
	}
	unit := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour}[s[len(s)-1]]
	n := int64(0)
	for _, ch := range s[:len(s)-1] {
		if ch < '0' || ch > '9' {
			unit = 0
			break
		}
		n = n*10 + int64(ch-'0')
	}
	if unit == 0 || n < 1 {
		return 0, errors.New("span must be a number of minutes, hours or days, such as 24h or 30d")
	}
	return time.Duration(n) * unit, nil
}

// Layout checks that a span in step is one a layout can have.
func Layout(step, span time.Duration) error {
	switch {
	case step <= 0 || DayWidth%step != 0:
		return errors.New("step must be 5m, 1h or 1d")
	case span < step || span > MaxSpan:
		return errors.New("span must be at least one step and at most 800d")
	case span/step >= MaxSlots:
		return errors.New("span holds too many steps; at most 999")
	}
	return nil
}

// Since is the earliest moment the series key holds, or the zero time.
func (r *Store) Since(key string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.series[key]; s != nil && s.first > 0 {
		return time.UnixMilli(s.first).UTC()
	}
	return time.Time{}
}

// Buckets lays the series key out over [from, to), as read at now, in
// slots step wide (five minutes, an hour or a day), aligned to UTC: the
// first slot holds from. Where the store keeps the series in buckets wider
// than step, as it does once they are old, a bucket is one slot as wide as
// it is. The last slot ends at to.
func (r *Store) Buckets(key string, from, to time.Time, step time.Duration, now time.Time) []model.Rollup {
	f, t, w := from.UnixMilli(), to.UnixMilli(), step.Milliseconds()
	if w <= 0 || day%w != 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.series[key]
	var bs []bucket
	var o *open
	if s != nil {
		bs, o = s.buckets, s.open
	}
	out := []model.Rollup{}
	cur := floor(f, w)
	i := sort.Search(len(bs), func(i int) bool { return bs[i].end() > cur })
	for cur < t {
		var a tally
		start, end := cur, cur+w
		if i < len(bs) && bs[i].start <= cur && bs[i].width > w {
			b := bs[i]
			start, end = b.start, b.end()
			for c, ms := range b.ms {
				a.ms[c] += float64(ms)
			}
			a.count(b)
			i++
		} else {
			for ; i < len(bs) && bs[i].start < end; i++ {
				for c, ms := range bs[i].ms {
					a.ms[c] += float64(ms)
				}
				a.count(bs[i])
			}
		}
		end = min(end, t)
		a.open(o, start, min(end, now.UnixMilli()))
		out = append(out, a.rollup(start, end-start))
		cur = end
	}
	return out
}

// A histogram counts what passing samples took in bins a quarter of a
// doubling wide: bin 0 holds less than a millisecond, and bin b ≥ 1 from
// 2^((b-1)/4) milliseconds up to the next. Bins merge exactly, so
// percentiles of any span come from the buckets it covers.
type histogram struct {
	n, sum, max int64
	bins        []bin // ascending, none empty
}

type bin struct {
	at uint8
	n  int64
}

// maxBin holds every sample of 2^(79/4) ms, some fifteen minutes, or more.
const maxBin = 80

func binOf(ms int64) uint8 {
	if ms < 1 {
		return 0
	}
	return uint8(min(maxBin, 1+int(math.Floor(4*math.Log2(float64(ms))))))
}

// floorOf is the shortest time a bin holds, in milliseconds.
func floorOf(b int) float64 {
	if b <= 0 {
		return 0
	}
	return math.Exp2(float64(b-1) / 4)
}

func (h *histogram) add(ms int64) {
	ms = max(ms, 0)
	h.n++
	h.sum += ms
	h.max = max(h.max, ms)
	h.put(binOf(ms), 1)
}

func (h *histogram) put(at uint8, n int64) {
	i, found := slices.BinarySearchFunc(h.bins, at, func(b bin, at uint8) int { return int(b.at) - int(at) })
	if found {
		h.bins[i].n += n
		return
	}
	h.bins = slices.Insert(h.bins, i, bin{at, n})
}

func (h *histogram) merge(o histogram) {
	if o.n == 0 {
		return
	}
	h.n += o.n
	h.sum += o.sum
	h.max = max(h.max, o.max)
	for _, b := range o.bins {
		h.put(b.at, b.n)
	}
}

// quantile estimates the q-th quantile: the nearest rank, placed within its
// bin as though the bin's samples were spread evenly, never past the
// slowest sample.
func (h *histogram) quantile(q float64) int64 {
	if h.n == 0 {
		return 0
	}
	rank := max(1, math.Ceil(q*float64(h.n)))
	seen := 0.0
	for _, b := range h.bins {
		if seen+float64(b.n) >= rank {
			lo, hi := floorOf(int(b.at)), floorOf(int(b.at)+1)
			return min(h.max, int64(math.Round(lo+(hi-lo)*(rank-seen)/float64(b.n))))
		}
		seen += float64(b.n)
	}
	return h.max
}

// What a store looks like on disk: each series by key, with its owner,
// its earliest moment, when it was last added to and its open sample
// (at, stale, class) in Unix milliseconds, and its buckets with start and width in seconds,
// the time of each class in milliseconds, the samples by outcome, and
// their latency as count, sum, slowest and then pairs of bin and count.
type disk struct {
	Schema int          `json:"schema"`
	Series []diskSeries `json:"series"`
}

type diskSeries struct {
	Key     string       `json:"k"`
	Owner   string       `json:"w,omitempty"`
	First   int64        `json:"f"`
	Updated int64        `json:"u"`
	Open    []int64      `json:"o,omitempty"`
	Buckets []diskBucket `json:"b"`
}

type diskBucket struct {
	S int64                `json:"s"`
	W int64                `json:"w"`
	T [classes]int64       `json:"t"`
	N [len(outcomes)]int64 `json:"n"`
	L []int64              `json:"l,omitempty"`
}

// MarshalJSON writes the store as it is kept on disk, series by key.
func (r *Store) MarshalJSON() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := disk{Schema: 1, Series: make([]diskSeries, 0, len(r.series))}
	for key, s := range r.series {
		ds := diskSeries{Key: key, Owner: s.owner, First: s.first, Updated: s.updated, Buckets: make([]diskBucket, 0, len(s.buckets))}
		if s.open != nil {
			ds.Open = []int64{s.open.at, s.open.stale, int64(s.open.class)}
		}
		for _, b := range s.buckets {
			db := diskBucket{S: b.start / 1000, W: b.width / 1000, T: b.ms, N: b.n}
			if b.lat.n > 0 {
				db.L = []int64{b.lat.n, b.lat.sum, b.lat.max}
				for _, x := range b.lat.bins {
					db.L = append(db.L, int64(x.at), x.n)
				}
			}
			ds.Buckets = append(ds.Buckets, db)
		}
		d.Series = append(d.Series, ds)
	}
	slices.SortFunc(d.Series, func(a, b diskSeries) int { return compareStrings(a.Key, b.Key) })
	return json.Marshal(d)
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

var errInvalid = errors.New("invalid rollups")

// UnmarshalJSON replaces what the store holds with what MarshalJSON wrote,
// refusing anything it could not have written. The store keeps its cap;
// one that no longer fits sheds when it next settles.
func (r *Store) UnmarshalJSON(raw []byte) error {
	var d disk
	if e := json.Unmarshal(raw, &d); e != nil || d.Schema != 1 {
		return errInvalid
	}
	held := map[string]*series{}
	size := int64(0)
	for _, ds := range d.Series {
		if ds.Key == "" || len(ds.Key) > 512 || len(ds.Owner) > 512 || held[ds.Key] != nil || ds.First < 0 || ds.Updated < 0 {
			return errInvalid
		}
		s := &series{owner: ds.Owner, first: ds.First, updated: ds.Updated, size: seriesSize(ds.Key)}
		if ds.Open != nil {
			if len(ds.Open) != 3 || ds.Open[0] <= 0 || ds.Open[1] <= 0 || ds.Open[2] < 0 || ds.Open[2] >= classes {
				return errInvalid
			}
			s.open = &open{ds.Open[0], ds.Open[1], int(ds.Open[2])}
		}
		end := int64(math.MinInt64)
		for _, db := range ds.Buckets {
			b := bucket{start: db.S * 1000, width: db.W * 1000, ms: db.T, n: db.N}
			if b.width != fine && b.width != hour && b.width != day || floor(b.start, b.width) != b.start || b.start < end {
				return errInvalid
			}
			end = b.end()
			for _, v := range append(b.ms[:], b.n[:]...) {
				if v < 0 {
					return errInvalid
				}
			}
			if len(db.L) > 0 {
				if len(db.L) < 5 || len(db.L)%2 == 0 || db.L[0] <= 0 || db.L[1] < 0 || db.L[2] < 0 {
					return errInvalid
				}
				b.lat = histogram{n: db.L[0], sum: db.L[1], max: db.L[2]}
				count := int64(0)
				for i := 3; i < len(db.L); i += 2 {
					at, n := db.L[i], db.L[i+1]
					if at < 0 || at > maxBin || n <= 0 || len(b.lat.bins) > 0 && int64(b.lat.bins[len(b.lat.bins)-1].at) >= at {
						return errInvalid
					}
					b.lat.bins = append(b.lat.bins, bin{uint8(at), n})
					count += n
				}
				if count != b.lat.n {
					return errInvalid
				}
			}
			s.buckets = append(s.buckets, b)
			s.size += b.size()
		}
		held[ds.Key] = s
		size += s.size
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.series, r.size = held, size
	return nil
}
