package rollup

import (
	"maps"
	"slices"
)

// Clone captures independent persistence state without encoding it. The caller
// can stream it while ingestion continues to update the live rollups.
func (r *Store) Clone() *Store {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := New(r.limit)
	out.size, out.reorder = r.size, r.reorder
	for key, s := range r.series {
		v := *s
		if s.open != nil {
			h := *s.open
			v.open = &h
		}
		v.recent = slices.Clone(s.recent)
		v.buckets = slices.Clone(s.buckets)
		for i := range v.buckets {
			b := &v.buckets[i]
			b.lat.bins = slices.Clone(b.lat.bins)
			for j := range b.ttfb {
				b.ttfb[j].bins = slices.Clone(b.ttfb[j].bins)
			}
			b.pops, b.cache = maps.Clone(b.pops), maps.Clone(b.cache)
		}
		out.series[key] = &v
	}
	return out
}
