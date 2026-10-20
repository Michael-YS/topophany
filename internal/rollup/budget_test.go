package rollup

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestActiveSeriesMetadataCannotExceedBudget(t *testing.T) {
	r := New(MinLimit)
	r.SetCurrent(func(string) bool { return true })
	for i := 0; i < 1000; i++ {
		r.Add(strings.Repeat("a", 64)+fmt.Sprintf("/check-%04d", i), strings.Repeat("o", 96), t0, "ok", 1, time.Minute, t0, Extra{})
	}
	check := func(r *Store) {
		t.Helper()
		raw, e := json.Marshal(r)
		if e != nil || int64(len(raw)) > r.Size() || r.Size() > r.limit {
			t.Fatalf("encoded=%d accounted=%d limit=%d err=%v", len(raw), r.Size(), r.limit, e)
		}
	}
	check(r)
	r.Settle(t0.Add(time.Hour))
	check(r)
	raw, _ := json.Marshal(r)
	back := New(MinLimit / 2)
	if e := json.Unmarshal(raw, back); e != nil {
		t.Fatal(e)
	}
	back.Settle(t0.Add(time.Hour))
	check(back)
}

func TestMetadataEvictionPreservesObserverShares(t *testing.T) {
	r := New(MinLimit)
	r.SetCurrent(func(string) bool { return true })
	r.Add("quiet", "quiet", t0, "ok", 1, time.Minute, t0, Extra{})
	for i := 0; i < 1000; i++ {
		r.Add(fmt.Sprintf("busy-%04d", i), "busy", t0, "ok", 1, time.Minute, t0, Extra{})
	}
	if r.Since("quiet").IsZero() || r.Size() > r.limit {
		t.Fatal("busy observer evicted quiet metadata", r.Size())
	}
}

// First-byte histograms, up to seventeen colo counts and the closed cache
// counts per bucket share the same cap: what is written never exceeds what
// the store accounted, before or after settling and reading it back.
func TestFirstByteAndLabelCountsStayWithinBudget(t *testing.T) {
	r := New(MinLimit)
	r.SetCurrent(func(string) bool { return true })
	colos := []string{"AAA", "BBB", "CCC", "DDD", "EEE", "FFF", "GGG", "HHH", "III", "JJJ", "KKK", "LLL", "MMM", "NNN", "OOO", "PPP", "QQQ", "RRR", "SSS", "TTT", "UUU", "VVV", "WWW", "XXX"}
	caches := []string{"HIT", "MISS", "EXPIRED", "STALE", "BYPASS", "REVALIDATED", "UPDATING", "DYNAMIC", "NONE", "UNKNOWN", "PRERENDER", "REFRESHHIT", "PASS", "ERROR", "REDIRECT", "LAMBDAGENERATEDRESPONSE", "FUNCTIONGENERATEDRESPONS", "not-a-cache"}
	check := func(r *Store) {
		t.Helper()
		raw, e := json.Marshal(r)
		if e != nil || int64(len(raw)) > r.Size() || r.Size() > r.limit {
			t.Fatalf("encoded=%d accounted=%d limit=%d err=%v", len(raw), r.Size(), r.limit, e)
		}
	}
	for i := 0; i < 1200; i++ {
		key := fmt.Sprintf("k/%02d", i%20)
		at := t0.Add(time.Duration(i) * time.Minute)
		r.Add(key, "obs", at, "ok", 1, time.Minute, at, Extra{
			FirstByteMS:  int64(i % 5000),
			HasFirstByte: true,
			HasEdge:      true,
			Cache:        caches[i%len(caches)],
			Pop:          colos[i%len(colos)],
		})
	}
	check(r)
	r.Settle(t0.Add(time.Hour))
	check(r)
	raw, _ := json.Marshal(r)
	back := New(MinLimit / 2)
	if e := json.Unmarshal(raw, back); e != nil {
		t.Fatal(e)
	}
	back.Settle(t0.Add(time.Hour))
	check(back)
}
