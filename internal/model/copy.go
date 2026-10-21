package model

import (
	"maps"
	"slices"
)

// CloneObservation detaches mutable evidence while sharing immutable strings.
func CloneObservation(v Observation) Observation {
	v.Facts = maps.Clone(v.Facts)
	v.Timings = maps.Clone(v.Timings)
	v.External = CloneExternalObservation(v.External)
	return v
}

// CloneBatch transfers ownership of all mutable members, including telemetry.
func CloneBatch(b Batch) Batch {
	b.Observations = slices.Clone(b.Observations)
	for i := range b.Observations {
		b.Observations[i] = CloneObservation(b.Observations[i])
	}
	if b.Heartbeat != nil {
		h := CloneHeartbeat(*b.Heartbeat)
		b.Heartbeat = &h
	}
	return b
}

// CloneExternalObservation detaches a source attempt and its report.
func CloneExternalObservation(e *ExternalObservation) *ExternalObservation {
	if e == nil {
		return nil
	}
	out := *e
	if e.Report != nil {
		r := CloneExternalReport(*e.Report)
		out.Report = &r
	}
	return &out
}
