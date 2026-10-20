package rollup

import (
	"errors"
	"maps"
	"math"
	"sort"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// tally adds up buckets and the time of an open sample.
type tally struct {
	ms           [classes]float64
	n            [len(outcomes)]int64
	lat          histogram
	ttfb         [ttfbClasses]histogram
	pops         map[string]int64
	cache        map[string]int64
	unreconciled int64
}

func (a *tally) count(b bucket) {
	a.unreconciled += b.unreconciled
	for i := range a.n {
		a.n[i] += b.n[i]
	}
	a.lat.merge(b.lat)
	for i := range a.ttfb {
		a.ttfb[i].merge(b.ttfb[i])
	}
	if len(b.pops) > 0 {
		if a.pops == nil {
			a.pops = map[string]int64{}
		}
		for k, n := range b.pops {
			a.pops[k] += n
		}
	}
	if len(b.cache) > 0 {
		if a.cache == nil {
			a.cache = map[string]int64{}
		}
		for k, n := range b.cache {
			a.cache[k] += n
		}
	}
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
	out := model.Rollup{Start: time.UnixMilli(start).UTC(), Seconds: secs, Unreconciled: a.unreconciled}
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
		out.Latency = latencyOf(a.lat)
	}
	// First byte is served by cache class. Unclassified samples are kept in
	// the store but not reported: the edge did not say which side answered.
	if a.ttfb[cached].n > 0 || a.ttfb[origin].n > 0 {
		fb := &model.FirstByteLatency{}
		if a.ttfb[cached].n > 0 {
			fb.Cached = latencyOf(a.ttfb[cached])
		}
		if a.ttfb[origin].n > 0 {
			fb.Origin = latencyOf(a.ttfb[origin])
		}
		out.FirstByte = fb
	}
	if len(a.pops) > 0 {
		// Cap once, on the whole span: a code small in every bucket can be
		// large over a day, so capping per bucket would lose it.
		capPops(a.pops)
		out.Pops = maps.Clone(a.pops)
	}
	if len(a.cache) > 0 {
		out.Cache = maps.Clone(a.cache)
	}
	return out
}

// latencyOf is what passing samples took, from a histogram.
func latencyOf(h histogram) *model.Latency {
	return &model.Latency{Samples: h.n, P50: h.quantile(.5), P95: h.quantile(.95), Max: h.max}
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
		} else {
			// Even a small overlap uses this bucket's time. Its missing
			// corrections must remain visible when sample counts are omitted.
			a.unreconciled += b.unreconciled
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
	if n > int64(MaxSpan/unit) {
		return 0, errors.New("span must be at most 800d")
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
