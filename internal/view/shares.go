package view

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// RollupWindow is the rollup a check's availability holds for window, or
// nil when that window is not reported.
func RollupWindow(list []model.Availability, window string) *model.Rollup {
	for i := range list {
		if list[i].Window == window {
			return &list[i].Rollup
		}
	}
	return nil
}

// FirstByteLine says what passing samples' first byte took, by the cache
// class the edge reported: "cached p50 40 ms · p95 90 ms · origin p50 600
// ms · p95 700 ms". An empty class is left out and no class is "".
func FirstByteLine(f *model.FirstByteLatency) string {
	if f == nil {
		return ""
	}
	parts := []string{}
	if f.Cached != nil {
		parts = append(parts, "cached "+latencyPair(*f.Cached))
	}
	if f.Origin != nil {
		parts = append(parts, "origin "+latencyPair(*f.Origin))
	}
	return strings.Join(parts, " · ")
}

func latencyPair(l model.Latency) string {
	return "p50 " + ms(l.P50) + " · p95 " + ms(l.P95)
}

// PopShares orders the colos a rollup counted by their share of all
// reported answers, largest first, as "LAX 92%". It lists at most limit
// when limit is positive. other is counted in the shares but never listed:
// it is not a colo. Nil when there is no named colo to show.
func PopShares(pops map[string]int64, limit int) []string {
	total := int64(0)
	for _, n := range pops {
		if n > 0 {
			total += n
		}
	}
	if total == 0 {
		return nil
	}
	type named struct {
		code string
		n    int64
	}
	list := []named{}
	for code, n := range pops {
		if code == "other" || n <= 0 || !model.IsColo(code) {
			continue
		}
		list = append(list, named{code, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].code < list[j].code
	})
	if len(list) == 0 {
		return nil
	}
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	out := make([]string, 0, len(list))
	for _, c := range list {
		// Never rounded up, like a check's up share: shares are lower
		// bounds, so independent roundings cannot add up to more than 100%.
		pct := int(math.Floor(100 * float64(c.n) / float64(total)))
		share := strconv.Itoa(pct) + "%"
		if pct < 1 {
			share = "<1%"
		}
		out = append(out, c.code+" "+share)
	}
	return out
}

// PopsLine lists the colo shares of a rollup, at most limit, for a line
// such as "LAX 92% · HKG 8%". other is omitted; see PopShares.
func PopsLine(pops map[string]int64, limit int) string {
	return strings.Join(PopShares(pops, limit), " · ")
}
