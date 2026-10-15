package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// textSlots is how many of a check's newest slots text shows.
const textSlots = 60

// samples writes a check's recent samples as its evidence panel shows them.
func (t *text) samples(s *view.Strip, failed string, now time.Time) {
	switch {
	case s == nil:
		t.line("RECENT SAMPLES")
		t.line(clean("unavailable: " + failed))
	case len(s.Slots) == 0:
		t.line("RECENT SAMPLES")
		t.line("none in the collector's history")
	default:
		t.line("RECENT SAMPLES · " + s.Span(now) + " · " + s.Every())
		t.line(view.Glyphs(s.Slots))
		if s.Latency.Samples > 0 {
			t.line(view.Bars(s.Slots))
		}
		t.line(s.Latency.String())
		t.line(view.StripLegend)
	}
	t.line()
}

func (a *App) history(ctx context.Context, r reader, check string, limit int, asJSON bool, now func() time.Time) error {
	h, server, e := r.history(ctx, check, limit)
	if e != nil {
		return e
	}
	if asJSON {
		return printJSON(a.Out, struct {
			client.History
			Server string `json:"server"`
			Check  string `json:"check"`
		}{h, server, check})
	}
	at := now()
	t := &text{w: a.Out}
	t.kv("Project", h.Project)
	t.kv("Collector", server)
	t.kv("Check", check)
	t.kv("Contract", short(h.Contract))
	t.kv("Samples", strconv.Itoa(len(h.Entries))+", newest first")
	t.line()
	cells := [][]string{{"SAMPLED", "AGE", "OUTCOME", "EVIDENCE", "STAGE", "CODE", "ELAPSED", "DETAIL"}}
	for _, o := range h.Entries {
		cells = append(cells, []string{o.SampledAt.Local().Format(view.StampLayout), view.Age(at, o.SampledAt), o.Outcome, o.Evidence, o.Stage, o.Code, strconv.FormatInt(o.DurationMS, 10) + " ms", o.Detail})
	}
	t.table(cells)
	return t.err
}

// rollups prints how a check fared, slot by slot, from the collector's
// rollups: the share of each slot measured and up, and what passing
// samples took.
func (a *App) rollups(ctx context.Context, r reader, check, step, span string, asJSON bool, now func() time.Time) error {
	h, server, e := r.rollups(ctx, check, step, span)
	if e != nil {
		return e
	}
	if asJSON {
		return printJSON(a.Out, struct {
			client.Rollups
			Server string `json:"server"`
		}{h, server})
	}
	t := &text{w: a.Out}
	t.kv("Project", h.Project)
	t.kv("Collector", server)
	t.kv("Check", check)
	t.kv("Contract", short(h.Contract))
	kept := "nothing yet"
	if !h.Since.IsZero() {
		kept = h.Since.Local().Format(view.SinceLayout) + " · " + view.Duration(now().Sub(h.Since)) + " ago"
	}
	t.kv("Kept since", kept)
	aligned := ""
	if h.Step%86400 == 0 {
		aligned = "; days begin at 00:00 UTC"
	}
	t.kv("Rollups", fmt.Sprintf("%s slots from %s, oldest first%s", slotSpan(h.Step), h.Total.Start.Local().Format(view.SinceLayout), aligned))
	t.kv("Total", view.RollupLine(h.Total))
	t.line()
	cells := [][]string{{"FROM", "FOR", "UP", "MEASURED", "DOWN", "DEGRADED", "INCONCLUSIVE", "SAMPLES", "P50", "P95"}}
	for _, s := range h.Slots {
		measured, p50, p95 := "", "", ""
		if s.Measured() > 0 {
			measured = view.MeasuredShare(s)
		}
		if s.Latency != nil {
			p50, p95 = strconv.FormatInt(s.Latency.P50, 10)+" ms", strconv.FormatInt(s.Latency.P95, 10)+" ms"
		}
		cells = append(cells, []string{s.Start.Local().Format(view.SinceLayout), slotSpan(s.Seconds), view.UpShare(s), measured, quiet(s.Down), quiet(s.Degraded), quiet(s.Unknown), strconv.FormatInt(s.Samples, 10), p50, p95})
	}
	t.table(cells)
	return t.err
}

// slotSpan is a slot's length as a step is named: "5m", "1h", "1d", or
// for a slot cut short by now, "37m".
func slotSpan(seconds int64) string {
	switch {
	case seconds > 0 && seconds%86400 == 0:
		return strconv.FormatInt(seconds/86400, 10) + "d"
	case seconds > 0 && seconds%3600 == 0:
		return strconv.FormatInt(seconds/3600, 10) + "h"
	}
	return view.Duration(time.Duration(seconds) * time.Second)
}

// quiet is a length of time, left blank when there is none.
func quiet(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return view.Duration(time.Duration(seconds) * time.Second)
}
