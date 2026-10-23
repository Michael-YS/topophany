package store

import (
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// Reserve a complete replay window, the largest declared report and cursor /
// counter metadata. JSON can encode one sample-ID byte as six bytes (e.g. '<').
// This is encoded state, not an RSS promise; raw rows and receipts need headroom.
func externalReservations(o Options) map[string]int64 {
	reservations := make(map[string]int64, len(o.Sources))
	for id := range o.Sources {
		reservations[id] = 2048
	}
	for _, b := range o.Bindings {
		size := o.Sources[b.Source].MaxReportBytes
		if size <= 0 {
			size = model.MaxReportBytes
		}
		reservations[b.Source] += int64(size + model.MaxSourceReportIDs*(128*6+64+32) + (16 << 10))
	}
	return reservations
}

func validateExternalReservations(o Options) error {
	if len(o.Bindings) == 0 {
		return nil
	}
	total := int64(externalStateOverhead)
	observers := map[string]int64{}
	for id, n := range externalReservations(o) {
		total += n
		observers[o.Sources[id].Observer] += n
	}
	if total > min(o.MaxStateBytes-1024, maxExternalStateBytes) {
		return fmt.Errorf("%w: complete external replay windows need %d encoded bytes; increase state-bytes or reduce external bindings", ErrCapacity, total)
	}
	if len(o.Observers) > 0 {
		for id, n := range observers {
			if n > o.byteShare(id) {
				return fmt.Errorf("%w: observer %s needs %d bytes for complete external replay windows; adjust storage weights or bindings", ErrScopeCapacity, id, n)
			}
		}
	}
	return nil
}

// ValidateTopology checks candidate allocations before a reload changes any
// generation or stops a scheduler. It performs no filesystem writes.
func (s *Store) ValidateTopology(c model.Config) error {
	s.mu.Lock()
	o := s.options
	s.mu.Unlock()
	o = o.WithTopology(c)
	o.Observers = nil
	for _, observer := range c.Observers {
		o.Observers = append(o.Observers, observer.ID)
	}
	o.ObserverWeights = config.StorageWeights(c)
	_, err := storageOptions(o)
	return err
}
