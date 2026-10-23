package engine

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// thresholds is the largest failure or recovery threshold a check can have.
const thresholds = 20

// Carry is what a check's samples leave for those after them, so that
// samples no longer held judge it as they did while they were: how many
// passed or failed in a row at the end, since when, and whether the check
// stood confirmed down. That last depends on its thresholds, which can be
// edited without changing what it measures, so it is kept for every pair
// a check can have: Down[f-1] has bit r-1 set when, with a failure
// threshold of f and a recovery threshold of r, the check stood confirmed
// down and not yet recovered. A carry belongs to one check under one
// contract, and holds the samples up to and including the one it names.
type Carry struct {
	// RulesVersion and Stale identify the evaluation rules used to fold this
	// prefix. Older checkpoints have no rules marker and must be reconfirmed
	// from retained evidence when loaded by a rule-aware owner.
	RulesVersion int           `json:"rules_version,omitempty"`
	Stale        time.Duration `json:"stale_ns,omitempty"`
	Check        string        `json:"check"`
	Contract     string        `json:"contract"`
	At           time.Time     `json:"at"`
	Boot         string        `json:"boot,omitempty"`
	Sequence     uint64        `json:"sequence,omitempty"`
	ID           string        `json:"id"`
	Failures     int           `json:"failures,omitempty"`
	Successes    int           `json:"successes,omitempty"`
	// PolicyRun is how many samples in a row at the end found a policy
	// violated. A policy violation clears Failures and Down, so without it
	// dropped samples would make a sustained violation look new.
	PolicyRun int                `json:"policy_run,omitempty"`
	Since     time.Time          `json:"since"`
	Down      [thresholds]uint32 `json:"down"`
}

// Compatible reports whether this summary can be reused under stale. The
// threshold bitmap supports all threshold pairs, so threshold edits do not
// invalidate it. Owners rebuild incompatible summaries from retained samples.
func (k Carry) Compatible(stale time.Duration) bool {
	return k.At.IsZero() || k.RulesVersion == 1 && k.Stale == stale
}

// earlier orders a check's samples: by when they were taken, then by
// sequence within a boot, then by ID.
func earlier(at time.Time, boot string, seq uint64, id string, v model.Observation) bool {
	if at.Equal(v.SampledAt) {
		if boot == v.Boot {
			return seq < v.Sequence
		}
		return id < v.ID
	}
	return at.Before(v.SampledAt)
}

// Holds reports whether v is among the samples folded into k: taken no
// later than the last of them.
func (k Carry) Holds(v model.Observation) bool {
	return !k.At.IsZero() && !earlier(k.At, k.Boot, k.Sequence, k.ID, v)
}

// Fold adds v, the next of its check's samples, to k. A gap longer than
// stale between two samples starts the count afresh; zero means none does.
func (k *Carry) Fold(v model.Observation, stale time.Duration) {
	k.RulesVersion, k.Stale = 1, stale
	if !k.At.IsZero() && stale > 0 && v.SampledAt.Sub(k.At) > stale {
		k.Failures, k.Successes, k.PolicyRun, k.Down = 0, 0, 0, [thresholds]uint32{}
	}
	k.Check, k.At, k.Boot, k.Sequence, k.ID = v.Check, v.SampledAt, v.Boot, v.Sequence, v.ID
	if v.Contract != "" {
		k.Contract = v.Contract
	}
	switch v.Outcome {
	case "ok":
		k.Successes++
		k.Failures, k.PolicyRun = 0, 0
		k.Since = v.SampledAt
		// Still down under every recovery threshold the run has not reached.
		for f := range k.Down {
			k.Down[f] &^= 1<<min(k.Successes, thresholds) - 1
		}
	case "fail":
		if k.Failures == 0 {
			k.Since = v.SampledAt
		}
		k.Failures++
		k.Successes, k.PolicyRun = 0, 0
		// Confirmed under every failure threshold the run has reached.
		for f := 0; f < min(k.Failures, thresholds); f++ {
			k.Down[f] = 1<<thresholds - 1
		}
	case "policy":
		// A policy violation ends whatever was confirmed, and counts its
		// own run: the engine needs it to tell a publication still loading
		// from a sustained drift.
		if k.PolicyRun == 0 {
			k.Since = v.SampledAt
		}
		k.PolicyRun++
		k.Failures, k.Successes, k.Down = 0, 0, [thresholds]uint32{}
	default:
		// A monitor-side error or an unknown outcome ends whatever was
		// confirmed and does not stand for a policy violation either.
		k.Failures, k.Successes, k.PolicyRun, k.Down = 0, 0, 0, [thresholds]uint32{}
		k.Since = v.SampledAt
	}
}

// down reports whether k stands confirmed down under a check's thresholds.
func (k Carry) down(failure, recovery int) bool {
	f, r := min(max(failure, 1), thresholds), min(max(recovery, 1), thresholds)
	return k.Down[f-1]&(1<<(r-1)) != 0
}
