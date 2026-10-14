package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A strip is a time axis: a slot per sample, whatever the order they come
// in, and a slot of no evidence for each interval in which none was taken,
// between samples and since the newest. A sample that is late by less than
// half an interval, as a scheduler's jitter makes it, leaves no hole.
func TestStripShowsWhereNoSampleWasTaken(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	x := model.Check{ID: "c", IntervalSeconds: 10}
	sample := func(ago time.Duration, outcome string, ms int64, timings map[string]int64) model.Observation {
		return model.Observation{Check: "c", SampledAt: now.Add(-ago), Outcome: outcome, DurationMS: ms, Timings: timings}
	}
	entries := []model.Observation{
		sample(31*time.Second, "ok", 40, map[string]int64{"connect_ms": 30, "dns_ms": 5}),
		sample(100*time.Second, "ok", 100, map[string]int64{"connect_ms": 90}),
		sample(89*time.Second, "fail", 5000, nil),
		sample(79*time.Second, "policy", 20, nil),
		sample(41*time.Second, "error", 0, nil),
		sample(110*time.Second, "unknown", 10, nil),
	}
	s := NewStrip(x, entries, now, 100)
	// 110s ago, 100s, 89s, 79s, then 38s with nothing until 41s, 31s, and
	// 31s since, which is two more intervals with nothing.
	if got, want := Glyphs(s.Slots), "○●✖▲···○●··"; got != want {
		t.Fatalf("strip %q, want %q", got, want)
	}
	if s.Samples != 6 || s.Interval != 10 || s.Slots[len(s.Slots)-3].SampledAt != now.Add(-31*time.Second) {
		t.Fatalf("strip counts %d samples every %ds", s.Samples, s.Interval)
	}
	if got, want := s.Latency.String(), "p50 40 ms · p95 100 ms · dns 5 ms · connect 30 ms"; got != want {
		t.Fatalf("latency %q, want %q", got, want)
	}
	if got := Bars(s.Slots); got != " █      ▄  " {
		t.Fatalf("bars %q", got)
	}
	if got := s.Span(now); got != "last 1m" {
		t.Fatalf("span %q", got)
	}
	// Cut to its newest slots, a strip counts only what is on it.
	s = NewStrip(x, entries, now, 4)
	if got := Glyphs(s.Slots); got != "○●··" || s.Samples != 2 || s.Latency.Samples != 1 || s.Latency.P50 != 40 {
		t.Fatalf("cut strip %q of %d samples, latency %+v", got, s.Samples, s.Latency)
	}
	// With no sample on it, a strip says how long it has been quiet.
	s = NewStrip(x, entries, now.Add(time.Hour), 6)
	if got := s.Span(now.Add(time.Hour)); Glyphs(s.Slots) != "······" || got != "no sample in the last 1m" || s.Latency.String() != "no passing samples" {
		t.Fatalf("quiet strip %q says %q", Glyphs(s.Slots), got)
	}
	// A long silence keeps a strip bounded.
	if s = NewStrip(x, entries, now.Add(1000*time.Hour), 1<<20); len(s.Slots) != maxSlots {
		t.Fatalf("strip of %d slots", len(s.Slots))
	}
	if NewStrip(x, nil, now, 60).Slots == nil || len(NewStrip(x, nil, now, 60).Slots) != 0 {
		t.Fatal("a check with no samples has an empty strip")
	}
	if !strings.Contains(StripLegend, "· no sample") {
		t.Fatal("legend does not explain the dots")
	}
}

// Jitter and probe time make a scheduler drift; a strip still gives each
// sample its own slot.
func TestStripAbsorbsSchedulerDrift(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	x := model.Check{ID: "c", IntervalSeconds: 60}
	entries := []model.Observation{}
	at := now
	for i := range 100 {
		at = at.Add(-time.Duration(60_000+[]int{-2900, 2900, 1500, 400}[i%4]+300) * time.Millisecond)
		entries = append(entries, model.Observation{Check: "c", SampledAt: at, Outcome: "ok"})
	}
	if got := Glyphs(NewStrip(x, entries, now, 200).Slots); got != strings.Repeat("●", 100) {
		t.Fatalf("drifting samples leave holes: %q", got)
	}
}
