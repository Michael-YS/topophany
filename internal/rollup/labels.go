package rollup

import (
	"sort"

	"github.com/WhitenWhiten/topophany/internal/adapters/edgeheaders"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// Classes of first-byte time, by what the edge said about the cache. A
// sample whose cache status is absent or outside both lists is
// unclassified: kept so the histograms stay separate, but not served,
// because the edge did not say which side answered.
const (
	cached = iota
	origin
	unclassified
	ttfbClasses
)

// cacheClass says whether a reported cache status describes an answer the
// edge served from its cache or one it fetched from the origin. The closed
// vocabulary is wider than these two lists; everything outside them,
// including no report at all, is unclassified.
func cacheClass(status string) int {
	switch status {
	case "HIT", "REVALIDATED", "STALE", "UPDATING":
		return cached
	case "MISS", "EXPIRED", "BYPASS", "DYNAMIC":
		return origin
	}
	return unclassified
}

// countCache counts one sample under the cache status the edge reported.
// Every sample is counted; an absent or unknown word is other. A key is
// kept only if the edge vocabulary knows it, so the map holds the closed
// words plus other.
func (b *bucket) countCache(status string) {
	if b.cache == nil {
		b.cache = map[string]int64{}
	}
	if !edgeheaders.CacheStatus(status) {
		status = "other"
	}
	b.cache[status]++
}

// otherPop is the key colo counts fall into when the edge did not report a
// well-formed colo, or when a bucket already holds its sixteen.
const otherPop = "other"

// maxPops is how many named colos one bucket, and one rollup window, keep;
// the rest merge into other. It bounds a window's output as well as a
// bucket's, so a month of buckets cannot leak an unbounded map.
const maxPops = 16

// countPop counts one sample under the colo the edge reported. An empty
// Pop is no report and is not counted. A malformed word counts as other. A
// well-formed code enters as a new name only when it is not rarer than the
// smallest named count, displacing that code into other; a new sample of a
// name already held only adds to it, so a code that becomes frequent can
// grow past the sixteen. Ties evict the lexicographically largest existing
// code, which keeps the choice independent of map order.
func (b *bucket) countPop(pop string) {
	if pop == "" {
		return
	}
	if b.pops == nil {
		b.pops = map[string]int64{}
	}
	key := pop
	if !model.IsColo(pop) {
		key = otherPop
	} else if _, held := b.pops[key]; !held && namedPops(b.pops) >= maxPops {
		victim := smallestPop(b.pops, key)
		if victim == "" || b.pops[victim] > 1 {
			// A first sample is no more frequent than the smallest held
			// count; it waits in other rather than displacing a busier code.
			key = otherPop
		} else {
			b.pops[otherPop] += b.pops[victim]
			delete(b.pops, victim)
		}
	}
	b.pops[key]++
}

// smallestPop is the named code with the smallest count, not counting
// other or except. Ties fall to the lexicographically largest code.
func smallestPop(m map[string]int64, except string) string {
	best := ""
	for k, n := range m {
		if k == otherPop || k == except {
			continue
		}
		if best == "" || n < m[best] || n == m[best] && k > best {
			best = k
		}
	}
	return best
}

// namedPops is how many real colo codes a map holds, not counting other.
func namedPops(m map[string]int64) int {
	n := len(m)
	if _, ok := m[otherPop]; ok {
		n--
	}
	return n
}

// mergePops adds another bucket's counts and keeps at most sixteen named
// codes, moving the smallest counts into other. Selecting by count, not by
// arrival, makes a merge deterministic; ties fall back to the code.
func (b *bucket) mergePops(o map[string]int64) {
	if len(o) == 0 {
		return
	}
	if b.pops == nil {
		b.pops = map[string]int64{}
	}
	for k, n := range o {
		b.pops[k] += n
	}
	capPops(b.pops)
}

// capPops keeps the sixteen named codes with the largest counts in m,
// adding every other named code's count to other.
func capPops(m map[string]int64) {
	if namedPops(m) <= maxPops {
		return
	}
	names := make([]string, 0, len(m))
	for k := range m {
		if k != otherPop {
			names = append(names, k)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	for _, k := range names[maxPops:] {
		m[otherPop] += m[k]
		delete(m, k)
	}
}

// validPops reports whether m is a colo count map the store could have
// written: at most sixteen named codes plus other, each named code exactly
// three uppercase letters, and a positive count for every entry (the
// writer never emits a zero).
func validPops(m map[string]int64) bool {
	named := 0
	for k, n := range m {
		if n <= 0 {
			return false
		}
		if k == otherPop {
			continue
		}
		if !model.IsColo(k) {
			return false
		}
		named++
	}
	return named <= maxPops && len(m) <= maxPops+1
}

// validCache reports whether m is a cache count map the store could have
// written: keys from the closed edge vocabulary or other, a positive count
// for every entry.
func validCache(m map[string]int64) bool {
	for k, n := range m {
		if n <= 0 || k != otherPop && !edgeheaders.CacheStatus(k) {
			return false
		}
	}
	return true
}
