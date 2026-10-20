package rollup

import (
	"encoding/json"
	"maps"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// The cache class of a first-byte sample is decided in one place: the four
// words that describe an answer the edge served, the four that describe one
// it fetched, and everything else, including no report, unclassified.
func TestCacheClassMapping(t *testing.T) {
	for _, tc := range []struct {
		word string
		want ttfbClass
	}{
		{"HIT", cached}, {"REVALIDATED", cached}, {"STALE", cached}, {"UPDATING", cached},
		{"MISS", origin}, {"EXPIRED", origin}, {"BYPASS", origin}, {"DYNAMIC", origin},
		{"NONE", unclassified}, {"UNKNOWN", unclassified}, {"PRERENDER", unclassified},
		{"", unclassified}, {"hit", unclassified}, {"HIT ", unclassified},
		{"SECRET", unclassified},
	} {
		if got := cacheClass(tc.word); got != tc.want {
			t.Errorf("cacheClass(%q) = %d, want %d", tc.word, got, tc.want)
		}
	}
}

// A cache status is counted only for a sample an edge answered. An edge
// answer without a status counts as other; a sample with no edge has no
// status to be absent, so it counts nothing, holds no map and costs what a
// bucket cost before cache counts existed.
func TestCacheCountsOnlyEdgeAnswers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome string
		extra   Extra
		want    map[string]int64
	}{
		{"no edge", "ok", Extra{}, nil},
		{"no edge, failing", "fail", Extra{}, nil},
		{"a status without an edge", "ok", Extra{Cache: "HIT"}, nil},
		{"edge without a status", "ok", Extra{HasEdge: true}, map[string]int64{"other": 1}},
		{"edge without a status, failing", "fail", Extra{HasEdge: true}, map[string]int64{"other": 1}},
		{"edge, unknown word", "ok", Extra{HasEdge: true, Cache: "NOPE"}, map[string]int64{"other": 1}},
		{"edge, HIT", "ok", Extra{HasEdge: true, Cache: "HIT"}, map[string]int64{"HIT": 1}},
		{"edge, MISS, failing", "fail", Extra{HasEdge: true, Cache: "MISS"}, map[string]int64{"MISS": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(MaxLimit)
			r.Add("c", "", t0, tc.outcome, 40, time.Minute, t0, tc.extra)
			b := r.series["c"].buckets[0]
			if !maps.Equal(b.cache, tc.want) || (tc.want == nil) != (b.cache == nil) {
				t.Fatalf("cache counted as %v, want %v", b.cache, tc.want)
			}
			// The baseline is the same sample from a probe with no edge: a
			// bucket and, for a passing sample, one latency bin.
			baseline := int64(bucketBase)
			if tc.outcome == "ok" {
				baseline += histogramOverhead + histogramBin
			}
			want := baseline
			if tc.want != nil {
				want += mapOverhead + mapEntry*int64(len(tc.want))
			}
			if got := b.size(); got != want {
				t.Fatalf("bucket size %d, want %d", got, want)
			}
			// What Add accounted is what a settle recounts.
			before := r.Size()
			r.Settle(t0)
			if r.Size() != before {
				t.Fatalf("size %d when added, %d when settled", before, r.Size())
			}
		})
	}
}

// ExtraOf says an edge answered when the observation names its provider,
// which every edge adapter does before any other label, and keeps the cache
// status of every provider and the colo of every provider but Vercel.
func TestExtraOfSaysWhetherAnEdgeAnswered(t *testing.T) {
	for _, tc := range []struct {
		name    string
		facts   map[string]string
		timings map[string]int64
		want    Extra
	}{
		{"no facts", nil, nil, Extra{}},
		{"a probe with no edge", map[string]string{"response_proof": "tls_handshake"}, nil, Extra{}},
		{"cloudflare", map[string]string{"edge_provider": "cloudflare", "edge_cache": "HIT", "edge_pop": "LAX"}, map[string]int64{"first_byte_ms": 40},
			Extra{FirstByteMS: 40, HasFirstByte: true, HasEdge: true, Cache: "HIT", Pop: "LAX"}},
		{"an edge that named no cache status", map[string]string{"edge_provider": "fastly"}, nil, Extra{HasEdge: true}},
		{"vercel keeps its cache status, not its region", map[string]string{"edge_provider": "vercel", "edge_cache": "MISS", "edge_pop": "hnd1"}, nil,
			Extra{HasEdge: true, Cache: "MISS"}},
		{"labels without a provider are not an edge", map[string]string{"edge_cache": "HIT"}, nil, Extra{Cache: "HIT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtraOf(model.Observation{Facts: tc.facts, Timings: tc.timings}); got != tc.want {
				t.Fatalf("ExtraOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Through ExtraOf, a check no edge answered writes the same rollups it wrote
// before cache counts: no cache map in a bucket, a window, a layout or the
// file, and the same size. A check an edge answered without a status pays
// for exactly one other entry in each bucket.
func TestChecksWithNoEdgeCarryNoCacheCounts(t *testing.T) {
	now := t0.Add(3 * time.Hour)
	fill := func(facts map[string]string) *Store {
		r := New(MaxLimit)
		for at := t0; at.Before(now); at = at.Add(5 * time.Minute) {
			r.Add("c", "obs", at, "ok", 20, 10*time.Minute, now, ExtraOf(model.Observation{Facts: facts}))
		}
		r.Settle(now)
		return r
	}
	plain, baseline := fill(map[string]string{"response_proof": "tls_handshake"}), New(MaxLimit)
	for at := t0; at.Before(now); at = at.Add(5 * time.Minute) {
		baseline.Add("c", "obs", at, "ok", 20, 10*time.Minute, now, Extra{})
	}
	baseline.Settle(now)
	if plain.Size() != baseline.Size() {
		t.Fatalf("a plain check costs %d, the baseline %d", plain.Size(), baseline.Size())
	}
	raw, e := json.Marshal(plain)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), `"c":{`) {
		t.Fatalf("a plain check wrote a cache map: %s", raw)
	}
	if got, _ := plain.Window("c", t0, now, now); got.Cache != nil || got.Samples != 36 {
		t.Fatalf("a plain check's window: %+v", got)
	}
	for i, slot := range plain.Buckets("c", t0, now, FineWidth, now) {
		if slot.Cache != nil {
			t.Fatalf("slot %d of a plain check carries %v", i, slot.Cache)
		}
	}

	edge := fill(map[string]string{"edge_provider": "cloudflare"})
	got, _ := edge.Window("c", t0, now, now)
	if got.Cache["other"] != 36 || len(got.Cache) != 1 {
		t.Fatalf("an edge answer without a status: %v", got.Cache)
	}
	if extra, buckets := edge.Size()-plain.Size(), int64(len(edge.series["c"].buckets)); extra != buckets*(mapOverhead+mapEntry) {
		t.Fatalf("an edge's other entry costs %d over %d buckets", extra, buckets)
	}
}

// Cache counts use the closed edge vocabulary the header parser keeps, and
// other for an absent or unknown word. Colo counts take three uppercase
// letters, cap a bucket at sixteen named codes plus other, and ignore a
// sample with no colo at all.
func TestCacheAndPopCountsKeepTheirVocabularies(t *testing.T) {
	words := []string{"HIT", "MISS", "EXPIRED", "STALE", "BYPASS", "REVALIDATED", "UPDATING", "DYNAMIC", "NONE", "UNKNOWN", "PRERENDER", "REFRESHHIT", "PASS", "ERROR", "REDIRECT", "LAMBDAGENERATEDRESPONSE", "FUNCTIONGENERATEDRESPONS"}
	for _, w := range words {
		var b bucket
		b.countCache(w)
		if b.cache[w] != 1 || len(b.cache) != 1 {
			t.Fatalf("cache word %q counted as %v", w, b.cache)
		}
	}
	for _, w := range []string{"", "hit", "SECRET"} {
		var b bucket
		b.countCache(w)
		if b.cache["other"] != 1 || len(b.cache) != 1 {
			t.Fatalf("unknown cache word %q counted as %v", w, b.cache)
		}
	}

	colos := []string{"AAA", "BBB", "CCC", "DDD", "EEE", "FFF", "GGG", "HHH", "III", "JJJ", "KKK", "LLL", "MMM", "NNN", "OOO", "PPP", "QQQ"}
	var b bucket
	for _, c := range colos {
		b.countPop(c)
	}
	b.countPop("lax") // malformed, other
	b.countPop("")    // no report, ignored
	if namedPops(b.pops) != 16 || len(b.pops) != 17 {
		t.Fatalf("pop keys were not capped: %v", b.pops)
	}
	if b.pops["other"] != 2 {
		t.Fatalf("other = %d, want the displaced code and the malformed word", b.pops["other"])
	}
}

// A code that is rare when it first appears but frequent later must still be
// able to enter the sixteen: a first sample may displace a one-off on equal
// counts, and later samples of that name then grow it.
func TestPopEvictionKeepsAFrequentLateCode(t *testing.T) {
	colos := []string{"AAA", "BBB", "CCC", "DDD", "EEE", "FFF", "GGG", "HHH", "III", "JJJ", "KKK", "LLL", "MMM", "NNN", "OOO", "PPP"}
	var b bucket
	for _, c := range colos {
		b.countPop(c)
	}
	for i := 0; i < 1000; i++ {
		b.countPop("ZZZ")
	}
	if b.pops["ZZZ"] != 1000 || namedPops(b.pops) != 16 || len(b.pops) != 17 {
		t.Fatalf("the late frequent code was lost: %v", b.pops)
	}
	if b.pops["other"] == 0 {
		t.Fatalf("the displaced one-off did not move to other: %v", b.pops)
	}
}

// A code small in every bucket but large across the window survives the
// window's one cap: capping per bucket would have dropped it into other.
func TestWindowKeepsACodeLargeAcrossBuckets(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(3 * time.Hour)
	first := []string{"AAA", "BBB", "CCC", "DDD", "EEE", "FFF", "GGG", "HHH", "III", "JJJ", "KKK", "LLL", "MMM", "NNN", "OOO", "PPP"}
	for _, code := range first {
		for i := 0; i < 100; i++ {
			r.Add("c", "", t0, "ok", 1, time.Minute, now, Extra{Pop: code})
		}
	}
	for i := 0; i < 2; i++ {
		r.Add("c", "", t0, "ok", 1, time.Minute, now, Extra{Pop: "not-a-colo"})
	}
	for i := 0; i < 100; i++ {
		at := t0.Add(time.Duration(i+5) * time.Minute)
		for j := 0; j < 2; j++ {
			r.Add("c", "", at, "ok", 1, time.Minute, now, Extra{Pop: "ZZZ"})
		}
	}
	got, _ := r.Window("c", t0, now, now)
	if got.Pops["ZZZ"] != 200 {
		t.Fatalf("the cross-bucket code is not named with its true count: %v", got.Pops)
	}
	if namedPops(got.Pops) != 16 || len(got.Pops) != 17 {
		t.Fatalf("the window was not capped once: %v", got.Pops)
	}
}

// Merging two capped buckets keeps the most frequent sixteen codes, moves
// the rest into other, and changes no total. Ties fall to the code.
func TestMergedPopsKeepTheMostFrequentCodes(t *testing.T) {
	a := map[string]int64{"AAA": 9, "BBB": 8, "CCC": 7, "DDD": 6, "EEE": 5, "FFF": 4, "GGG": 3, "HHH": 2, "III": 1}
	b := map[string]int64{"AAA": 1, "JJJ": 10, "KKK": 9, "LLL": 8, "MMM": 7, "NNN": 6, "OOO": 5, "PPP": 4, "QQQ": 3, "RRR": 2, "SSS": 1}
	var merged bucket
	merged.mergePops(a)
	merged.mergePops(b)
	if namedPops(merged.pops) != 16 || len(merged.pops) != 17 {
		t.Fatalf("merged map %v", merged.pops)
	}
	total := int64(0)
	for _, n := range merged.pops {
		total += n
	}
	want := int64(0)
	for _, m := range []map[string]int64{a, b} {
		for _, n := range m {
			want += n
		}
	}
	if total != want {
		t.Fatalf("merge changed the total: %d, want %d", total, want)
	}
	if merged.pops["AAA"] != 10 || merged.pops["JJJ"] != 10 || merged.pops["SSS"] != 0 {
		t.Fatalf("most frequent codes were not kept: %v", merged.pops)
	}
}

// A bucket's accounted size covers every byte the disk projection can
// write, maps, first-byte bins and all.
func TestBucketSizeBoundsItsDiskBytes(t *testing.T) {
	b := bucket{start: 0, width: fine}
	for c := range b.n {
		b.n[c] = math.MaxInt64
	}
	for c := range b.ms {
		b.ms[c] = math.MaxInt64
	}
	for _, h := range []*histogram{&b.lat, &b.ttfb[cached], &b.ttfb[origin], &b.ttfb[unclassified]} {
		for i := 1; i <= 80; i++ {
			// The widest decimal each count can take, in every bin.
			h.bins = append(h.bins, bin{uint8(i), math.MaxInt64})
			h.n, h.sum, h.max = math.MaxInt64, math.MaxInt64, math.MaxInt64
		}
	}
	b.pops = map[string]int64{"other": math.MaxInt64}
	for code := 0; code < 16; code++ {
		b.pops[string(rune('A'+code))+"AA"] = math.MaxInt64
	}
	b.cache = map[string]int64{"other": math.MaxInt64}
	for _, w := range []string{"LAMBDAGENERATEDRESPONSE", "FUNCTIONGENERATEDRESPONS", "REVALIDATED", "PRERENDER", "REFRESHHIT"} {
		b.cache[w] = math.MaxInt64
	}
	raw, err := json.Marshal(diskOfBucket(b))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) > b.size() {
		t.Fatalf("encoded %d bytes but accounted %d", len(raw), b.size())
	}
}

// Validators refuse what the store could not have written: a colo key with
// the wrong shape, a cache key outside the vocabulary, a negative count,
// and more named colos than a bucket keeps.
func TestStoredLabelMapsAreValidated(t *testing.T) {
	if !validPops(nil) || !validCache(nil) || !validPops(map[string]int64{"LAX": 1, "other": 2}) {
		t.Fatal("a valid map was refused")
	}
	for _, m := range []map[string]int64{{"lax": 1}, {"LA": 1}, {"LAXX": 1}, {"LAX": -1}, {"LAX": 0}, {"other": -1}} {
		if validPops(m) {
			t.Fatalf("invalid pop map accepted: %v", m)
		}
	}
	tooMany := map[string]int64{"other": 1}
	for code := 0; code < 17; code++ {
		tooMany[string(rune('A'+code))+"AA"] = 1
	}
	if validPops(tooMany) {
		t.Fatal("seventeen named colos accepted")
	}
	for _, m := range []map[string]int64{{"hit": 1}, {"SECRET": 1}, {"HIT": -1}, {"HIT": 0}} {
		if validCache(m) {
			t.Fatalf("invalid cache map accepted: %v", m)
		}
	}
}

// A window's first byte and labels follow the same partial-bucket rule as
// its samples: a bucket the window barely overlaps contributes time, but
// no counts.
func TestWindowLabelsFollowThePartialBucketRule(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(time.Hour)
	for _, at := range []time.Time{t0, t0.Add(time.Minute)} {
		r.Add("c", "", at, "ok", 50, 10*time.Minute, now, Extra{FirstByteMS: 40, HasFirstByte: true, HasEdge: true, Cache: "HIT", Pop: "LAX"})
	}
	// The window holds the first fifth of the bucket: time only.
	partial, _ := r.Window("c", t0.Add(4*time.Minute), now, now)
	if partial.FirstByte != nil || partial.Pops != nil || partial.Cache != nil || partial.Samples != 0 {
		t.Fatalf("a partial bucket contributed counts: %+v", partial)
	}
	if partial.Up == 0 {
		t.Fatal("a partial bucket contributed no time")
	}
	// Two thirds of the bucket: counts included.
	whole, _ := r.Window("c", t0.Add(2*time.Minute), now, now)
	if whole.FirstByte == nil || whole.FirstByte.Cached == nil || whole.FirstByte.Cached.Samples != 2 || whole.Pops["LAX"] != 2 || whole.Cache["HIT"] != 2 {
		t.Fatalf("a counted bucket lost its labels: %+v", whole)
	}
}
