package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
)

func TestLocalExternalAdmissionAdvancesDuringRollback(t *testing.T) {
	c := externalStoreConfig(t)
	wall := time.Now().UTC()
	start, tick := wall, wall
	opts := externalStoreOptions(c, &wall)
	opts.MonotonicNow = func() time.Time { return tick }
	s := openTest(t, t.TempDir(), opts)
	for i, elapsed := range []time.Duration{0, time.Minute, 10 * time.Minute} {
		tick = start.Add(elapsed)
		wall = tick
		if i > 0 {
			wall = tick.Add(-time.Hour)
		}
		v := externalStoreObservation(t, c, fmt.Sprint(i), 25, uint64(i+1), tick, tick)
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatalf("local collector rejected normal time after rollback: %v", err)
		}
	}
	far := tick.Add(time.Hour)
	v := externalStoreObservation(t, c, "future", 25, 99, far, far)
	if err := s.Put(batch(c, v)); admission.Code(err) != "sample_time_out_of_range" {
		t.Fatalf("local collector admitted future timestamp: %v", err)
	}
	if s.ExternalView().Bindings["vm-resources"].ReportSamples != 3 {
		t.Fatal("clock admission lost or invented reports")
	}
}
