package view

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A check's evidence shows its recent samples as a strip on a time axis:
// one slot per sample, oldest first, and a slot of no evidence for each
// interval in which none was taken, between samples and since the newest.
// Unknown time is never drawn as up or down.

// StripSamples is how many samples the evidence of a check reads.
const StripSamples = 120

// maxSlots bounds a strip; a longer silence keeps only its newest slots.
const maxSlots = 2 * StripSamples

// Slot is one place on a strip: the outcome of the sample taken there (ok,
// fail, policy, error or unknown), when and how long it took, or "none"
// where no sample was taken.
type Slot struct {
	Outcome   string    `json:"outcome"`
	SampledAt time.Time `json:"sampled_at,omitzero"`
	ElapsedMS int64     `json:"elapsed_ms,omitempty"`
}

// Strip is a check's recent samples on a time axis.
type Strip struct {
	Check    string  `json:"check"`
	Interval int     `json:"interval_seconds"`
	Samples  int     `json:"samples"`
	Slots    []Slot  `json:"slots"`
	Latency  Latency `json:"latency"`
}

// Latency is what the passing samples on a strip took: the median and the
// 95th percentile, and the median of each phase the probe timed.
type Latency struct {
	Samples int     `json:"samples"`
	P50     int64   `json:"p50_ms"`
	P95     int64   `json:"p95_ms"`
	Phases  []Phase `json:"phases,omitempty"`
}

// Phase is one part of a probe's elapsed time, e.g. its DNS lookup.
type Phase struct {
	Name string `json:"name"`
	P50  int64  `json:"p50_ms"`
}

// phases are the timings a probe reports, in the order they happen.
var phases = []struct{ key, name string }{{"dns_ms", "dns"}, {"connect_ms", "connect"}, {"tls_ms", "tls"}, {"first_byte_ms", "first byte"}}

// NewStrip lays out a check's samples, in any order, as read at now, in
// at most n slots: the newest. A check that runs every interval misses a
// slot when the next sample is more than half an interval late. What the
// strip counts and its latency are of the samples on it.
func NewStrip(x model.Check, entries []model.Observation, now time.Time, n int) Strip {
	interval := time.Duration(max(1, x.IntervalSeconds)) * time.Second
	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, func(a, b model.Observation) int { return a.SampledAt.Compare(b.SampledAt) })
	// missed is how many slots passed between a and b with no sample.
	missed := func(a, b time.Time) int {
		n := math.Round(float64(b.Sub(a))/float64(interval)) - 1
		return int(min(max(n, 0), maxSlots))
	}
	slots := []Slot{}
	kept := []model.Observation{}
	none := func(n int) {
		for range n {
			slots = append(slots, Slot{Outcome: "none"})
			kept = append(kept, model.Observation{})
		}
	}
	for i, o := range sorted {
		if i > 0 {
			none(missed(sorted[i-1].SampledAt, o.SampledAt))
		}
		slots = append(slots, Slot{Outcome: o.Outcome, SampledAt: o.SampledAt, ElapsedMS: o.DurationMS})
		kept = append(kept, o)
	}
	if len(sorted) > 0 {
		none(missed(sorted[len(sorted)-1].SampledAt, now))
	}
	if n = min(max(n, 0), maxSlots); len(slots) > n {
		slots, kept = slots[len(slots)-n:], kept[len(kept)-n:]
	}
	s := Strip{Check: x.ID, Interval: int(interval / time.Second), Slots: slots}
	var took []int64
	timed := map[string][]int64{}
	for i, sl := range slots {
		if sl.Outcome == "none" {
			continue
		}
		s.Samples++
		if sl.Outcome != "ok" {
			continue
		}
		took = append(took, sl.ElapsedMS)
		for _, p := range phases {
			if ms, ok := kept[i].Timings[p.key]; ok {
				timed[p.name] = append(timed[p.name], ms)
			}
		}
	}
	s.Latency = Latency{Samples: len(took), P50: percentile(took, .5), P95: percentile(took, .95)}
	for _, p := range phases {
		if len(timed[p.name]) > 0 {
			s.Latency.Phases = append(s.Latency.Phases, Phase{p.name, percentile(timed[p.name], .5)})
		}
	}
	return s
}

// percentile is the nearest-rank percentile of ms, 0 when there are none.
func percentile(ms []int64, p float64) int64 {
	if len(ms) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(ms))
	return sorted[max(0, int(math.Ceil(p*float64(len(sorted))))-1)]
}

// SlotHealth is the health a slot's outcome reads as: "" where no sample
// was taken.
func SlotHealth(outcome string) string {
	switch outcome {
	case "ok":
		return "up"
	case "fail":
		return "down"
	case "policy":
		return "degraded"
	case "none":
		return ""
	}
	return "unknown"
}

// SlotGlyph is how a slot is drawn: the glyph of the health it reads as,
// or a dot where no sample was taken.
func SlotGlyph(outcome string) string {
	switch SlotHealth(outcome) {
	case "up":
		return "●"
	case "down":
		return "✖"
	case "degraded":
		return "▲"
	case "":
		return "·"
	}
	return "○"
}

// Glyphs draws slots as SlotGlyph does, one column each.
func Glyphs(slots []Slot) string {
	var b strings.Builder
	for _, sl := range slots {
		b.WriteString(SlotGlyph(sl.Outcome))
	}
	return b.String()
}

// bars are the heights of a latency bar, lowest first.
var bars = []rune("▁▂▃▄▅▆▇█")

// Bars draws what each passing sample among slots took as a bar, from
// nothing to the slowest of them; a slot with no passing sample is blank,
// and one that took no time, or less, has the lowest bar.
func Bars(slots []Slot) string {
	slowest := int64(0)
	for _, sl := range slots {
		if sl.Outcome == "ok" {
			slowest = max(slowest, sl.ElapsedMS)
		}
	}
	out := make([]rune, len(slots))
	for i, sl := range slots {
		switch {
		case sl.Outcome != "ok":
			out[i] = ' '
		case slowest == 0:
			out[i] = bars[0]
		default:
			out[i] = bars[min(len(bars)-1, max(0, int(math.Round(float64(len(bars)-1)*float64(sl.ElapsedMS)/float64(slowest)))))]
		}
	}
	return string(out)
}

// Span is how long a strip covers as read at now: from its oldest sample,
// "last 32m", or with none on it, "no sample in the last 10m".
func (s Strip) Span(now time.Time) string {
	for _, sl := range s.Slots {
		if !sl.SampledAt.IsZero() {
			return "last " + Duration(now.Sub(sl.SampledAt))
		}
	}
	return "no sample in the last " + Duration(time.Duration(len(s.Slots)*s.Interval)*time.Second)
}

// Every is how often a strip's check runs, a slot each time: "one slot
// per 30s".
func (s Strip) Every() string {
	return "one slot per " + Duration(time.Duration(s.Interval)*time.Second)
}

// Duration is a length of time to the second under a minute, then to the
// minute: "45s", "32m", "2h 5m", "3d 4h".
func Duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return itoa(max(0, int(d.Seconds()))) + "s"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h " + itoa(int(d.Minutes())%60) + "m"
	}
	return itoa(int(d.Hours())/24) + "d " + itoa(int(d.Hours())%24) + "h"
}

// String says what passing samples took: "p50 120 ms · p95 340 ms · dns
// 12 ms · connect 30 ms".
func (l Latency) String() string {
	if l.Samples == 0 {
		return "no passing samples"
	}
	parts := []string{"p50 " + ms(l.P50), "p95 " + ms(l.P95)}
	for _, p := range l.Phases {
		parts = append(parts, p.Name+" "+ms(p.P50))
	}
	return strings.Join(parts, " · ")
}

func ms(n int64) string { return strconv.FormatInt(n, 10) + " ms" }

// StripLegend says what the glyphs on a strip mean.
const StripLegend = "● ok  ▲ policy  ✖ fail  ○ error or unknown  · no sample; oldest left"
