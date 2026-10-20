package rollup

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"slices"
	"time"
)

// maxMillis is the latest moment a file may name, in Unix milliseconds: the
// last whole second of the year 9999, the latest a time.Time encodes as JSON.
// A moment is read back as a time in Since and in the slots of a layout, so
// one beyond it would make every snapshot that includes it fail to encode.
// The writer stamps real clocks, so it never writes one; the reader refuses
// it as corruption rather than clamping it. It also keeps every sum of two
// moments, a start and a width or a sample and how long it stays current,
// far from overflowing an int64.
var maxMillis = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC).UnixMilli()

// moment reports whether ms is a moment the writer could have written.
func moment(ms int64) bool { return ms >= 0 && ms <= maxMillis }

// What a store looks like on disk: each series by key, with its owner,
// its earliest moment, when it was last added to and its open sample
// (at, stale, class) in Unix milliseconds, and its buckets with start and width in seconds,
// the time of each class in milliseconds, the samples by outcome, and
// their latency as count, sum, slowest and then pairs of bin and count.
// A bucket may also carry the optional keys fb, p and c: fb the first-byte
// time by cache class, as exactly three lists in the shape of the latency
// (cached, origin, unclassified), p the colo counts and c the cache status
// counts. A file without them reads as a bucket with none of those.
type disk struct {
	Schema int          `json:"schema"`
	Series []diskSeries `json:"series"`
}

type diskSeries struct {
	Key            string       `json:"k"`
	Owner          string       `json:"w,omitempty"`
	First          int64        `json:"f"`
	Updated        int64        `json:"u"`
	Open           []int64      `json:"o,omitempty"`
	Buckets        []diskBucket `json:"b"`
	Recent         [][3]int64   `json:"r,omitempty"`
	CorrectionFrom int64        `json:"c,omitempty"`
}

type diskBucket struct {
	S  int64                `json:"s"`
	W  int64                `json:"w"`
	T  [classes]int64       `json:"t"`
	N  [len(outcomes)]int64 `json:"n"`
	L  []int64              `json:"l,omitempty"`  // latency of passing samples
	Fb [][]int64            `json:"fb,omitempty"` // first byte by cached, origin, unclassified
	P  map[string]int64     `json:"p,omitempty"`  // colo counts
	C  map[string]int64     `json:"c,omitempty"`  // cache status counts
	U  int64                `json:"u,omitempty"`
}

// encodeHistogram writes a histogram as its count, sum, slowest sample and
// then bin/count pairs, the one shape both latency and first-byte fields
// use. An empty histogram is nil so omitempty drops it.
func encodeHistogram(h histogram) []int64 {
	if h.n == 0 {
		return nil
	}
	out := make([]int64, 3, 3+2*len(h.bins))
	out[0], out[1], out[2] = h.n, h.sum, h.max
	for _, x := range h.bins {
		out = append(out, int64(x.at), x.n)
	}
	return out
}

// decodeHistogram reads what encodeHistogram wrote, refusing anything the
// store could not have written. An empty list is a histogram with no
// samples.
func decodeHistogram(raw []int64) (histogram, bool) {
	if len(raw) == 0 {
		return histogram{}, true
	}
	if len(raw) < 5 || len(raw)%2 == 0 || raw[0] <= 0 || raw[1] < 0 || raw[2] < 0 {
		return histogram{}, false
	}
	h := histogram{n: raw[0], sum: raw[1], max: raw[2], bins: make([]bin, 0, (len(raw)-3)/2)}
	count := int64(0)
	for i := 3; i < len(raw); i += 2 {
		at, n := raw[i], raw[i+1]
		if at < 0 || at > maxBin || n <= 0 || len(h.bins) > 0 && int64(h.bins[len(h.bins)-1].at) >= at {
			return histogram{}, false
		}
		h.bins = append(h.bins, bin{uint8(at), n})
		count += n
	}
	if count != h.n {
		return histogram{}, false
	}
	return h, true
}

// diskOfBucket is the one projection of a bucket to disk, used by both the
// buffered and the streaming writers so their fields cannot drift.
func diskOfBucket(b bucket) diskBucket {
	db := diskBucket{S: b.start / 1000, W: b.width / 1000, T: b.ms, N: b.n, L: encodeHistogram(b.lat), P: b.pops, C: b.cache, U: b.unreconciled}
	for i := range b.ttfb {
		if b.ttfb[i].n > 0 {
			if db.Fb == nil {
				db.Fb = make([][]int64, ttfbClasses)
				for j := range db.Fb {
					db.Fb[j] = []int64{}
				}
			}
			db.Fb[i] = encodeHistogram(b.ttfb[i])
		}
	}
	return db
}

// WriteJSON emits the same schema as MarshalJSON, with bounded temporary
// storage even when a single series holds most of the rollup budget.
func (r *Store) WriteJSON(w io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var err error
	text := func(s string) {
		if err == nil {
			_, err = io.WriteString(w, s)
		}
	}
	enc := json.NewEncoder(w)
	value := func(v any) {
		if err == nil {
			err = enc.Encode(v)
		}
	}
	text(`{"schema":1,"series":[`)
	for i, key := range slices.Sorted(maps.Keys(r.series)) {
		if err != nil {
			break
		}
		if i > 0 {
			text(",")
		}
		s := r.series[key]
		text(`{"k":`)
		value(key)
		text(`,"w":`)
		value(s.owner)
		text(`,"f":`)
		value(s.first)
		text(`,"u":`)
		value(s.updated)
		if s.open != nil {
			text(`,"o":`)
			value([3]int64{s.open.at, s.open.stale, int64(s.open.class)})
		}
		text(`,"b":[`)
		for j, b := range s.buckets {
			if err != nil {
				break
			}
			if j > 0 {
				text(",")
			}
			value(diskOfBucket(b))
		}
		text(`]`)
		if len(s.recent) > 0 {
			text(`,"r":[`)
			for j, p := range s.recent {
				if err != nil {
					break
				}
				if j > 0 {
					text(",")
				}
				value([3]int64{p.at, p.stale, int64(p.class)})
			}
			text(`]`)
		}
		text(`,"c":`)
		value(s.correctionFrom)
		text("}")
	}
	text("]}")
	return err
}

// MarshalJSON writes the store as it is kept on disk, series by key.
func (r *Store) MarshalJSON() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := disk{Schema: 1, Series: make([]diskSeries, 0, len(r.series))}
	for key, s := range r.series {
		ds := diskSeries{Key: key, Owner: s.owner, First: s.first, Updated: s.updated, Buckets: make([]diskBucket, 0, len(s.buckets))}
		ds.CorrectionFrom = s.correctionFrom
		for _, point := range s.recent {
			ds.Recent = append(ds.Recent, [3]int64{point.at, point.stale, int64(point.class)})
		}
		if s.open != nil {
			ds.Open = []int64{s.open.at, s.open.stale, int64(s.open.class)}
		}
		for _, b := range s.buckets {
			ds.Buckets = append(ds.Buckets, diskOfBucket(b))
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
	size := int64(diskBytes)
	for _, ds := range d.Series {
		if ds.Key == "" || len(ds.Key) > 512 || len(ds.Owner) > 512 || held[ds.Key] != nil || !moment(ds.First) || !moment(ds.Updated) {
			return errInvalid
		}
		s := &series{owner: ds.Owner, first: ds.First, updated: ds.Updated, size: seriesSize(ds.Key, ds.Owner), buckets: make([]bucket, 0, len(ds.Buckets)), recent: make([]open, 0, len(ds.Recent))}
		if ds.Open != nil {
			// Its stale time is bounded like a moment, so at plus stale cannot
			// overflow.
			if len(ds.Open) != 3 || ds.Open[0] <= 0 || ds.Open[1] <= 0 || !moment(ds.Open[0]) || !moment(ds.Open[1]) || ds.Open[2] < 0 || ds.Open[2] >= classes {
				return errInvalid
			}
			s.open = &open{ds.Open[0], ds.Open[1], int(ds.Open[2])}
		}
		if !moment(ds.CorrectionFrom) {
			return errInvalid
		}
		s.correctionFrom = ds.CorrectionFrom
		for _, point := range ds.Recent {
			if point[0] <= 0 || point[1] <= 0 || !moment(point[0]) || !moment(point[1]) || point[2] < 0 || point[2] >= classes || len(s.recent) > 0 && point[0] <= s.recent[len(s.recent)-1].at {
				return errInvalid
			}
			s.recent = append(s.recent, open{point[0], point[1], int(point[2])})
		}
		if len(s.recent) > 0 && (s.open == nil || *s.open != s.recent[len(s.recent)-1]) {
			return errInvalid
		}
		if len(s.recent) == 0 && s.open != nil {
			// Older checkpoints have only their newest boundary. Do not
			// pretend earlier intervals can be reconstructed from totals.
			s.recent = []open{*s.open}
			s.correctionFrom = max(s.correctionFrom, s.open.at)
		}
		s.size += int64(len(s.recent)) * boundaryBytes
		end := int64(math.MinInt64)
		for _, db := range ds.Buckets {
			// Start and width are seconds on disk. Check them as seconds,
			// before they become milliseconds: a value near 2^61 would wrap
			// to a plausible one when multiplied. A start is a moment, and a
			// width is one of the three the writer makes, none wider than a
			// day, so a bucket's end is after its start and cannot overflow.
			if db.S < 0 || db.S > maxMillis/1000 || db.W != fine/1000 && db.W != hour/1000 && db.W != day/1000 {
				return errInvalid
			}
			b := bucket{start: db.S * 1000, width: db.W * 1000, ms: db.T, n: db.N, unreconciled: db.U}
			if floor(b.start, b.width) != b.start || b.start < end {
				return errInvalid
			}
			end = b.end()
			for _, v := range append(b.ms[:], b.n[:]...) {
				if v < 0 {
					return errInvalid
				}
			}
			counted := int64(0)
			for _, n := range b.n {
				counted += n
			}
			if b.unreconciled < 0 || b.unreconciled > counted {
				return errInvalid
			}
			if h, ok := decodeHistogram(db.L); !ok {
				return errInvalid
			} else {
				b.lat = h
			}
			// fb is absent or exactly three histogram lists in class order;
			// the writer emits all three once any class has a sample, with
			// empty lists for the others. A shorter list is not a shape this
			// writer can produce, so it is refused rather than guessed at.
			if db.Fb != nil && len(db.Fb) != ttfbClasses {
				return errInvalid
			}
			for i, raw := range db.Fb {
				h, ok := decodeHistogram(raw)
				if !ok {
					return errInvalid
				}
				b.ttfb[i] = h
			}
			if !validPops(db.P) || !validCache(db.C) {
				return errInvalid
			}
			b.pops, b.cache = db.P, db.C
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
