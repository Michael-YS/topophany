package store

import (
	"errors"
	"maps"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/rollup"
)

func storageOptions(o Options) (Options, error) {
	o.Observers = append([]string(nil), o.Observers...)
	o.ObserverWeights = maps.Clone(o.ObserverWeights)
	o.Bindings = maps.Clone(o.Bindings)
	o.Sources = maps.Clone(o.Sources)
	if o.MaxHistory == 0 {
		o.MaxHistory = 20000
	}
	if o.MaxReceipts == 0 {
		o.MaxReceipts = 500000
	}
	if o.Retention == 0 {
		o.Retention = 24 * time.Hour
	}
	if o.CheckpointBytes == 0 {
		o.CheckpointBytes = 16 << 20
		if o.MaxJournalBytes == 0 {
			o.MaxJournalBytes = 128 << 20
		}
	}
	// Explicit old-style thresholds remain fixed unless a ceiling is supplied.
	if o.MaxJournalBytes == 0 {
		o.MaxJournalBytes = o.CheckpointBytes
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxStateBytes == 0 {
		o.MaxStateBytes = 128 << 20
	}
	if o.RollupBytes == 0 {
		o.RollupBytes = 32 << 20
	}
	if len(o.Bindings) > model.MaxExternalChecks || len(o.Sources) > model.MaxSources {
		return o, errors.New("external binding/source budget exceeded")
	}
	if o.MaxHistory < 1 || o.MaxReceipts < 1 || o.Retention < time.Minute || o.CheckpointBytes < 1024 || o.CheckpointBytes > 128<<20 || o.MaxJournalBytes < o.CheckpointBytes || o.MaxJournalBytes > 128<<20 || o.MaxStateBytes < 1024 || o.MaxStateBytes > 128<<20 || o.RollupBytes < rollup.MinLimit || o.RollupBytes > rollup.MaxLimit {
		return o, errors.New("invalid storage limits")
	}

	seen := map[string]bool{}
	for _, id := range o.Observers {
		if seen[id] {
			return o, errors.New("duplicate storage observer")
		}
		seen[id] = true
	}
	for id, w := range o.ObserverWeights {
		if !seen[id] || w < 1 || w > 1000 {
			return o, errors.New("invalid storage weight")
		}
	}
	o.weightTotal = 0
	if o.ObserverWeights == nil {
		o.ObserverWeights = map[string]int{}
	}
	for _, id := range o.Observers {
		w := max(o.ObserverWeights[id], 1)
		o.ObserverWeights[id] = w
		o.weightTotal += int64(w)
	}
	return o, nil
}

// Budgets computes allocation without creating or opening any state files.
func Budgets(o Options) (Usage, error) {
	o, err := storageOptions(o)
	if err != nil {
		return Usage{}, err
	}
	s := &Store{options: o, seen: map[string]receipt{}, scopeBytes: map[string]int64{}, scopeReceipts: map[string]int{}}
	return s.Usage(), nil
}
func (o Options) share(total int64, id string) int64 {
	if len(o.Observers) == 0 {
		return total
	}
	return total * int64(o.ObserverWeights[id]) / o.weightTotal
}
func (o Options) byteShare(id string) int64 {
	return o.share(max(o.MaxStateBytes-1024-int64(len(o.Observers))*model.HeartbeatReservation, 0), id)
}
