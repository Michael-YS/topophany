package model

import (
	"maps"
	"slices"
)

// CloneObservation detaches mutable evidence while sharing immutable strings.
func CloneObservation(v Observation) Observation {
	v.Facts = maps.Clone(v.Facts)
	v.Timings = maps.Clone(v.Timings)
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
