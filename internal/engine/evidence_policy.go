package engine

import "time"

// EvidencePolicy separates what was measured from rules applied to retained
// samples. Display fields do not enter either identity; threshold pairs are
// already represented by Carry.Down and need no invalidation.
type EvidencePolicy struct {
	Contract string
	Stale    time.Duration
}

type EvidenceChange uint8

const (
	PreserveEvidence EvidenceChange = iota
	RebuildConfirmation
	ResetMeasurement
)

func (previous EvidencePolicy) ChangeTo(next EvidencePolicy) EvidenceChange {
	if previous.Contract != next.Contract {
		return ResetMeasurement
	}
	if previous.Stale != next.Stale {
		return RebuildConfirmation
	}
	return PreserveEvidence
}
