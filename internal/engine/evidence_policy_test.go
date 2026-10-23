package engine

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
	"time"
)

func TestEvidencePolicySeparatesMeasurementAndConfirmation(t *testing.T) {
	previous := EvidencePolicy{Contract: "measurement", Stale: time.Minute}
	for _, tc := range []struct {
		next   EvidencePolicy
		change EvidenceChange
	}{
		{previous, PreserveEvidence},
		{EvidencePolicy{Contract: "measurement", Stale: 2 * time.Minute}, RebuildConfirmation},
		{EvidencePolicy{Contract: "other", Stale: time.Minute}, ResetMeasurement},
	} {
		if got := previous.ChangeTo(tc.next); got != tc.change {
			t.Errorf("change=%v want=%v", got, tc.change)
		}
		var carry Carry
		carry.Fold(model.Observation{ID: "one", Check: "check", Contract: previous.Contract, SampledAt: time.Now(), Outcome: "fail"}, previous.Stale)
		if got := carry.Compatible(tc.next); got != (tc.change == PreserveEvidence) {
			t.Errorf("carry reuse=%v change=%v", got, tc.change)
		}
	}
}
