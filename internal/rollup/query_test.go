package rollup

import (
	"maps"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// What a window and a layout return owns its colo and cache maps: a caller
// that edits them, as a view that re-sorts or trims them might, changes
// nothing the store holds and nothing a later read returns.
func TestRollupMapsAreNotTheStoresOwn(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(time.Hour)
	for _, cache := range []string{"HIT", "MISS"} {
		r.Add("c", "", t0.Add(time.Minute), "ok", 5, 10*time.Minute, now, Extra{HasEdge: true, Cache: cache, Pop: "LAX"})
	}
	held := r.series["c"].buckets[0]
	wantPops, wantCache := maps.Clone(held.pops), maps.Clone(held.cache)
	read := func() []model.Rollup {
		got, _ := r.Window("c", t0, now, now)
		return append([]model.Rollup{got}, r.Buckets("c", t0, now, FineWidth, now)...)
	}
	for _, got := range read() {
		if got.Samples == 0 {
			continue
		}
		got.Pops["LAX"] = 99
		got.Pops["ZZZ"] = 1
		delete(got.Cache, "HIT")
		got.Cache["PASS"] = 7
	}
	held = r.series["c"].buckets[0]
	if !maps.Equal(held.pops, wantPops) || !maps.Equal(held.cache, wantCache) {
		t.Fatalf("a reader changed the store's maps: %v %v", held.pops, held.cache)
	}
	for _, got := range read() {
		if got.Samples != 0 && (got.Pops["LAX"] != 2 || got.Cache["HIT"] != 1 || got.Cache["MISS"] != 1 || len(got.Pops) != 1 || len(got.Cache) != 2) {
			t.Fatalf("a later read saw an earlier reader's edit: %+v", got)
		}
	}
}

func TestParseSpanBounds(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{"1m", time.Minute}, {"90m", 90 * time.Minute}, {"24h", 24 * time.Hour},
		{"800d", MaxSpan}, {"19200h", MaxSpan}, {"1152000m", MaxSpan},
		{"000001m", time.Minute},
		{"", 0}, {"1", 0}, {"0d", 0}, {"1w", 0}, {"1H", 0}, {"+1h", 0},
		{"-1h", 0}, {"1.5h", 0}, {" 1h", 0}, {"1h ", 0}, {"１h", 0},
		{"801d", 0}, {"19201h", 0}, {"1152001m", 0}, {"213504d", 0},
		{"213505d", 0}, {"9999999h", 0}, {"99999999m", 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseSpan(tc.input)
			if tc.want == 0 && err == nil || tc.want != 0 && (err != nil || got != tc.want) {
				t.Fatalf("ParseSpan(%q) = %s, %v; want %s", tc.input, got, err, tc.want)
			}
		})
	}
}
