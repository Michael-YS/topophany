package rollup

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"slices"
)

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
	S int64                `json:"s"`
	W int64                `json:"w"`
	T [classes]int64       `json:"t"`
	N [len(outcomes)]int64 `json:"n"`
	L []int64              `json:"l,omitempty"`
	U int64                `json:"u,omitempty"`
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
			db := diskBucket{S: b.start / 1000, W: b.width / 1000, T: b.ms, N: b.n, U: b.unreconciled}
			if b.lat.n > 0 {
				db.L = []int64{b.lat.n, b.lat.sum, b.lat.max}
				for _, x := range b.lat.bins {
					db.L = append(db.L, int64(x.at), x.n)
				}
			}
			value(db)
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
			db := diskBucket{S: b.start / 1000, W: b.width / 1000, T: b.ms, N: b.n, U: b.unreconciled}
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
	size := int64(diskBytes)
	for _, ds := range d.Series {
		if ds.Key == "" || len(ds.Key) > 512 || len(ds.Owner) > 512 || held[ds.Key] != nil || ds.First < 0 || ds.Updated < 0 {
			return errInvalid
		}
		s := &series{owner: ds.Owner, first: ds.First, updated: ds.Updated, size: seriesSize(ds.Key, ds.Owner), buckets: make([]bucket, 0, len(ds.Buckets)), recent: make([]open, 0, len(ds.Recent))}
		if ds.Open != nil {
			if len(ds.Open) != 3 || ds.Open[0] <= 0 || ds.Open[1] <= 0 || ds.Open[2] < 0 || ds.Open[2] >= classes {
				return errInvalid
			}
			s.open = &open{ds.Open[0], ds.Open[1], int(ds.Open[2])}
		}
		if ds.CorrectionFrom < 0 {
			return errInvalid
		}
		s.correctionFrom = ds.CorrectionFrom
		for _, point := range ds.Recent {
			if point[0] <= 0 || point[1] <= 0 || point[2] < 0 || point[2] >= classes || len(s.recent) > 0 && point[0] <= s.recent[len(s.recent)-1].at {
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
			b := bucket{start: db.S * 1000, width: db.W * 1000, ms: db.T, n: db.N, unreconciled: db.U}
			if b.width != fine && b.width != hour && b.width != day || floor(b.start, b.width) != b.start || b.start < end {
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
			if len(db.L) > 0 {
				if len(db.L) < 5 || len(db.L)%2 == 0 || db.L[0] <= 0 || db.L[1] < 0 || db.L[2] < 0 {
					return errInvalid
				}
				b.lat = histogram{n: db.L[0], sum: db.L[1], max: db.L[2]}
				b.lat.bins = make([]bin, 0, (len(db.L)-3)/2)
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
