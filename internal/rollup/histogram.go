package rollup

import (
	"math"
	"slices"
)

// A histogram counts what passing samples took in bins a quarter of a
// doubling wide: bin 0 holds less than a millisecond, and bin b ≥ 1 from
// 2^((b-1)/4) milliseconds up to the next. Bins merge exactly, so
// percentiles of any span come from the buckets it covers.
type histogram struct {
	n, sum, max int64
	bins        []bin // ascending, none empty
}

type bin struct {
	at uint8
	n  int64
}

// maxBin holds every sample of 2^(79/4) ms, some fifteen minutes, or more.
const maxBin = 80

func binOf(ms int64) uint8 {
	if ms < 1 {
		return 0
	}
	return uint8(min(maxBin, 1+int(math.Floor(4*math.Log2(float64(ms))))))
}

// floorOf is the shortest time a bin holds, in milliseconds.
func floorOf(b int) float64 {
	if b <= 0 {
		return 0
	}
	return math.Exp2(float64(b-1) / 4)
}

func (h *histogram) add(ms int64) {
	ms = max(ms, 0)
	h.n++
	h.sum += ms
	h.max = max(h.max, ms)
	h.put(binOf(ms), 1)
}

func (h *histogram) put(at uint8, n int64) {
	i, found := slices.BinarySearchFunc(h.bins, at, func(b bin, at uint8) int { return int(b.at) - int(at) })
	if found {
		h.bins[i].n += n
		return
	}
	h.bins = slices.Insert(h.bins, i, bin{at, n})
}

func (h *histogram) merge(o histogram) {
	if o.n == 0 {
		return
	}
	h.n += o.n
	h.sum += o.sum
	h.max = max(h.max, o.max)
	for _, b := range o.bins {
		h.put(b.at, b.n)
	}
}

// quantile estimates the q-th quantile: the nearest rank, placed within its
// bin as though the bin's samples were spread evenly, never past the
// slowest sample.
func (h *histogram) quantile(q float64) int64 {
	if h.n == 0 {
		return 0
	}
	rank := max(1, math.Ceil(q*float64(h.n)))
	seen := 0.0
	for _, b := range h.bins {
		if seen+float64(b.n) >= rank {
			lo, hi := floorOf(int(b.at)), floorOf(int(b.at)+1)
			return min(h.max, int64(math.Round(lo+(hi-lo)*(rank-seen)/float64(b.n))))
		}
		seen += float64(b.n)
	}
	return h.max
}
