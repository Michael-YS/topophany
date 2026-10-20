package rollup

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// The cache class of a first-byte sample is decided in one place: the four
// words that describe an answer the edge served, the four that describe one
// it fetched, and everything else, including no report, unclassified.
func TestCacheClassMapping(t *testing.T) {
	for _, tc := range []struct {
		word string
		want int
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
		r.Add("c", "", at, "ok", 50, 10*time.Minute, now, Extra{FirstByteMS: 40, HasFirstByte: true, Cache: "HIT", Pop: "LAX"})
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
