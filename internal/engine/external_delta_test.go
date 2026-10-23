package engine

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestPreparedExternalDeltaOwnsChangedEvidence(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeResource, nil)
	now := time.Now().UTC()
	plan := NewPlan(c)
	original := plan.PrepareExternal(nil, nil, nil, externalState(t, c, now, resourceReport(now, 25), now))
	changed := externalState(t, c, now, resourceReport(now, 50), now)
	next := original.WithExternalChanges(changed.Sources, changed.Bindings)
	*changed.Bindings["external-check"].Last.Resource.CPU.Percent = 99
	for _, tc := range []struct {
		prepared *Prepared
		want     float64
	}{{original, 25}, {next, 50}} {
		snap := tc.prepared.Build("test", "", now)
		if got := *snap.External[0].Report.Resource.CPU.Percent; got != tc.want {
			t.Fatal(got, tc.want)
		}
		*snap.External[0].Report.Resource.CPU.Percent = 0
		if got := *tc.prepared.Build("test", "", now).External[0].Report.Resource.CPU.Percent; got != tc.want {
			t.Fatal("snapshot aliases evidence")
		}
	}
}
