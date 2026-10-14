package view

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// How a check fared over time comes from the collector's rollups: each
// sample's outcome stands until the next sample or until it goes stale.
// Up is a share of the measured time, the time a current sample said
// whether the check passed; measured is a share of the whole span. Time
// no current sample stood for is never counted as up or down.

// UpShare is the share of a rollup's measured time that was up, never
// rounded up: 99.96% reads "99.9%", and only all of it reads "100%". It is
// "" when nothing was measured.
func UpShare(r model.Rollup) string {
	m := r.Measured()
	switch {
	case m <= 0:
		return ""
	case r.Up >= m:
		return "100%"
	}
	return strconv.FormatFloat(math.Floor(1000*float64(r.Up)/float64(m))/10, 'f', 1, 64) + "%"
}

// MeasuredShare is the share of a rollup's span that was measured, to the
// nearest percent, with a sliver as "<1%" and only all of it as "100%".
func MeasuredShare(r model.Rollup) string {
	m := r.Measured()
	switch {
	case m <= 0 || r.Seconds <= 0:
		return "0%"
	case m >= r.Seconds:
		return "100%"
	}
	p := int(math.Round(100 * float64(m) / float64(r.Seconds)))
	switch {
	case p < 1:
		return "<1%"
	case p > 99:
		return "99%"
	}
	return strconv.Itoa(p) + "%"
}

// SinceLayout writes when a check's rollups begin, in local time.
const SinceLayout = "2006-01-02 15:04"

// RollupLine says how a check fared over a span: "99.2% up over 96%
// measured · down 11m · degraded 2m · inconclusive 1m", where degraded is
// time a policy was found violated and inconclusive time the monitor could
// not tell; "not measured" when no current sample stood for any of it.
func RollupLine(r model.Rollup) string {
	parts := []string{"not measured"}
	if r.Measured() > 0 {
		parts = []string{UpShare(r) + " up over " + MeasuredShare(r) + " measured"}
	}
	for _, p := range []struct {
		word    string
		seconds int64
	}{{"down", r.Down}, {"degraded", r.Degraded}, {"inconclusive", r.Unknown}} {
		if p.seconds > 0 {
			parts = append(parts, p.word+" "+Duration(time.Duration(p.seconds)*time.Second))
		}
	}
	return strings.Join(parts, " · ")
}

// AvailabilityLine is RollupLine for a window, noting when the rollups
// begin if that is inside it: "… · kept since 2026-10-08 14:00".
func AvailabilityLine(a model.Availability) string {
	line := RollupLine(a.Rollup)
	if !a.Since.IsZero() {
		line += " · kept since " + a.Since.Local().Format(SinceLayout)
	}
	return line
}

// AvailabilityDetails are the lines a check's evidence shows of how it
// fared: one per window, and how to read them.
func AvailabilityDetails(list []model.Availability) []string {
	if len(list) == 0 {
		return nil
	}
	out := []string{"Availability:"}
	for _, a := range list {
		out = append(out, "Last "+a.Window+": "+AvailabilityLine(a))
	}
	return append(out, "Each sample's outcome stands until the next sample or until it goes stale; time no current sample stood for is unmeasured, neither up nor down.")
}

// Up24h is the share of the last day's measured time a check was up, as
// the Checks tab shows it, or "" when nothing was measured.
func Up24h(list []model.Availability) string {
	for _, a := range list {
		if a.Window == "24h" {
			return UpShare(a.Rollup)
		}
	}
	return ""
}
