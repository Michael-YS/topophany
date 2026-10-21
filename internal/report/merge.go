package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Verdict is the collector's acceptance classification of one new source
// observation. Duplicate, late, conflict and unusable observations never
// advance the binding's last report, deadline or confirmation.
type Verdict string

const (
	VerdictNew       Verdict = "new"
	VerdictDuplicate Verdict = "duplicate"
	VerdictLate      Verdict = "late"
	VerdictConflict  Verdict = "conflict"
	VerdictUnusable  Verdict = "unusable"
)

// MergeResult explains a verdict without exposing collector-owned state to
// the sender. BasisChanged says the upstream measurement basis changed and
// previous confirmation runs were reset.
type MergeResult struct {
	Verdict      Verdict
	Code         string
	BasisChanged bool
}

// ContentHash is the canonical content digest used to distinguish a same-
// identity revision from a duplicate. It is not a sample identity.
func ContentHash(r model.ExternalReport) string {
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// SampleIdentity derives the preferred identity. It reports false when no
// stable identity and no known time exist, making the report unusable for
// freshness and confirmation.
func SampleIdentity(r model.ExternalReport, sampledAt time.Time) (mode, identity string, ok bool) {
	switch {
	case r.SampleID != "":
		return "sample", "sample:" + r.SampleID, true
	case r.Epoch != "" || r.Sequence != nil:
		epoch := r.Epoch
		if r.Sequence == nil {
			return "", "", false
		}
		return "sequence", "sequence:" + epoch + ":" + uintString(*r.Sequence), true
	case sampledAt.IsZero():
		return "", "", false
	default:
		return "time", "time:" + r.TimeBasis + ":" + sampledAt.UTC().Format(time.RFC3339Nano), true
	}
}

func uintString(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// BasisSignature captures the upstream units/calculation vocabulary. A
// change resets confirmation runs but does not erase historical counters.
func BasisSignature(r model.ExternalReport) string {
	parts := []string{}
	if res := r.Resource; res != nil {
		if c := res.CPU; c != nil {
			parts = append(parts, "cpu:"+c.Basis)
		}
		if m := res.Memory; m != nil {
			parts = append(parts, "memory:"+m.Basis)
		}
		for _, d := range res.Disks {
			parts = append(parts, "disk:"+d.ID+":"+d.Basis)
		}
	}
	if s := r.Status; s != nil {
		parts = append(parts, "status")
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// MergeBinding applies one normalized report to a binding's persisted state.
// It is pure and deterministic: replay with the same state and report yields
// the same verdict. Every identity, content, order, time and usability
// decision is made before accepted state changes. Duplicate, late, conflict
// and unusable paths advance only their diagnostic counters and last-result
// fields; they never change the cursor, last report, deadline, basis,
// confirmation runs or accepted-sample statistics.
func MergeBinding(check, contract string, binding model.ExternalBinding, state model.BindingState, r model.ExternalReport, receivedAt time.Time) (model.BindingState, MergeResult) {
	failure, recovery := state.FailureThreshold, state.RecoveryThreshold
	if failure < 1 {
		failure = 3
	}
	if recovery < 1 {
		recovery = 2
	}
	policyThreshold := max(state.PolicyThreshold, 1)
	if state.Check == "" || state.Contract != contract || state.Source != binding.Source || state.Object != binding.Object || state.ReportType != binding.Report || state.MaxAgeSeconds != binding.MaxAgeSeconds {
		state = model.BindingState{Check: check, Contract: contract, Source: binding.Source, Object: binding.Object, ReportType: binding.Report, MaxAgeSeconds: binding.MaxAgeSeconds, FailureThreshold: failure, RecoveryThreshold: recovery, PolicyThreshold: state.PolicyThreshold}
	}
	sampledAt, timeKnown := SampleAt(r)
	mode, identity, identityOK := SampleIdentity(r, sampledAt)
	if !timeKnown || !identityOK {
		state.Unusable = addCount(state.Unusable)
		state.LastResult, state.LastCode = string(VerdictUnusable), "unknown_report_time"
		return state, MergeResult{Verdict: VerdictUnusable, Code: "unknown_report_time"}
	}
	hash := ContentHash(r)
	verdict, code := classifyReport(state, r, hash, mode, identity, sampledAt)
	if verdict != VerdictNew {
		switch verdict {
		case VerdictDuplicate:
			state.Duplicates = addCount(state.Duplicates)
		case VerdictLate:
			state.Late = addCount(state.Late)
		case VerdictConflict:
			state.Conflicts = addCount(state.Conflicts)
		case VerdictUnusable:
			state.Unusable = addCount(state.Unusable)
		}
		state.LastResult, state.LastCode = string(verdict), code
		return state, MergeResult{Verdict: verdict, Code: code}
	}

	basis := BasisSignature(r)
	basisChanged := state.Basis != "" && state.Basis != basis
	epochChanged := state.ReportSamples > 0 && r.Epoch != state.Cursor.Epoch
	gap := evidenceGap(state, binding, sampledAt)
	if epochChanged || basisChanged {
		resetContinuity(&state)
	} else if gap {
		// A gap restarts consecutive runs but does not itself prove a
		// recovery or a new failure: confirmed flags change only through a
		// report that is accepted below.
		resetRuns(&state)
	}
	if epochChanged {
		retireEpoch(&state, state.Cursor.Epoch)
		unretireEpoch(&state, r.Epoch)
	}
	state.Cursor = model.ReportCursor{Mode: mode, Identity: identity, SampleID: r.SampleID, Epoch: r.Epoch, Sequence: cloneUint(r.Sequence), ObservedAt: sampledAt, TimeBasis: r.TimeBasis, Hash: hash}
	last := model.CloneExternalReport(r)
	state.Last = &last
	state.DataDeadline = sampledAt.Add(time.Duration(binding.MaxAgeSeconds) * time.Second)
	state.Basis = basis
	if state.FirstAcceptedAt.IsZero() {
		state.FirstAcceptedAt = receivedAt
	}
	state.LastAcceptedAt = receivedAt
	state.ReportSamples = addCount(state.ReportSamples)
	if r.SampleID != "" {
		// Own the slice: a caller's backing array must not be written past its
		// current length, and the bound keeps the replay memory finite.
		seen := append([]model.SeenSample(nil), state.Seen...)
		seen = append(seen, model.SeenSample{ID: r.SampleID, Hash: hash})
		if len(seen) > model.MaxSourceReportIDs {
			seen = append([]model.SeenSample(nil), seen[len(seen)-model.MaxSourceReportIDs:]...)
		}
		state.Seen = seen
	}
	switch {
	case r.Resource != nil:
		state.ResourceSamples = addCount(state.ResourceSamples)
		state.UpRun, state.DownRun = 0, 0
		policy := Evaluate(binding, r)
		switch policy.Outcome {
		case PolicyViolation:
			state.PolicyRun = addCount(state.PolicyRun)
			state.PolicyViolations = addCount(state.PolicyViolations)
			state.PolicyConfirmed = state.PolicyConfirmed || state.PolicyRun >= int64(policyThreshold)
		case PolicyUnknown:
			state.PolicyRun = 0
			state.PolicyUnknowns = addCount(state.PolicyUnknowns)
			state.PolicyConfirmed = false
		default:
			state.PolicyRun = 0
			state.PolicyConfirmed = false
		}
	case r.Status != nil:
		state.StatusSamples = addCount(state.StatusSamples)
		state.PolicyRun = 0
		switch r.Status.Status {
		case "up":
			state.UpRun = addCount(state.UpRun)
			state.DownRun = 0
			state.StatusUp = addCount(state.StatusUp)
			if state.ConfirmedDown && state.UpRun >= int64(recovery) {
				state.ConfirmedDown = false
			}
		case "down":
			state.DownRun = addCount(state.DownRun)
			state.UpRun = 0
			state.StatusDown = addCount(state.StatusDown)
			if state.DownRun >= int64(failure) {
				state.ConfirmedDown = true
			}
		case "degraded":
			state.UpRun, state.DownRun = 0, 0
			state.ConfirmedDown = false
			state.StatusDegraded = addCount(state.StatusDegraded)
		default:
			state.UpRun, state.DownRun = 0, 0
			state.ConfirmedDown = false
			state.StatusUnknown = addCount(state.StatusUnknown)
		}
		if r.Status.UpstreamLatencyMS != nil {
			ms := *r.Status.UpstreamLatencyMS
			state.UpstreamLatencySamples = addCount(state.UpstreamLatencySamples)
			if state.UpstreamLatencySumMS > math.MaxInt64-ms {
				state.UpstreamLatencySumMS = math.MaxInt64
			} else {
				state.UpstreamLatencySumMS += ms
			}
			if ms > state.UpstreamLatencyMaxMS {
				state.UpstreamLatencyMaxMS = ms
			}
		}
	}
	state.LastResult, state.LastCode = string(VerdictNew), ""
	return state, MergeResult{Verdict: VerdictNew, BasisChanged: basisChanged}
}

// classifyReport returns the verdict without mutating accepted state.
func classifyReport(state model.BindingState, r model.ExternalReport, hash, mode, identity string, sampledAt time.Time) (Verdict, string) {
	// A previously seen sample ID is definitive even after the cursor moved.
	if r.SampleID != "" {
		for _, seen := range state.Seen {
			if seen.ID != r.SampleID {
				continue
			}
			if seen.Hash == hash {
				return VerdictDuplicate, "seen_sample"
			}
			return VerdictConflict, "same_sample_different_content"
		}
	}
	if r.Epoch != "" && slices.Contains(state.RetiredEpochs, r.Epoch) {
		return VerdictLate, "retired_epoch"
	}
	if state.Cursor.Identity != "" && state.Cursor.Identity == identity {
		if state.Cursor.Hash == hash {
			return VerdictDuplicate, "same_identity"
		}
		return VerdictConflict, "same_identity_different_content"
	}
	return orderVerdict(state.Cursor, mode, sampledAt, r)
}

// resetContinuity starts a new consecutive-confirmation lifecycle. Historical
// accepted-sample counters are deliberately untouched.
func resetContinuity(state *model.BindingState) {
	resetRuns(state)
	state.ConfirmedDown, state.PolicyConfirmed = false, false
}

func resetRuns(state *model.BindingState) {
	state.UpRun, state.DownRun, state.PolicyRun = 0, 0, 0
}

// evidenceGap reports whether the previous accepted report had already left
// its validity window when this sample was taken. Equality is a gap: a
// report's data deadline is exclusive.
func evidenceGap(state model.BindingState, binding model.ExternalBinding, sampledAt time.Time) bool {
	if state.Last == nil {
		return false
	}
	prev, ok := SampleAt(*state.Last)
	if !ok {
		return false
	}
	maxAge := time.Duration(binding.MaxAgeSeconds) * time.Second
	if maxAge <= 0 {
		maxAge = time.Duration(state.MaxAgeSeconds) * time.Second
	}
	if maxAge <= 0 {
		return false
	}
	return !sampledAt.Before(prev.Add(maxAge))
}

// retireEpoch records the end of a producer sequence namespace and keeps the
// list bounded. The current cursor's epoch is only retired after a newer epoch
// is accepted, never by a rejected report.
func retireEpoch(state *model.BindingState, epoch string) {
	if epoch == "" || slices.Contains(state.RetiredEpochs, epoch) {
		return
	}
	retired := append([]string(nil), state.RetiredEpochs...)
	retired = append(retired, epoch)
	if len(retired) > model.MaxRetiredEpochs {
		retired = append([]string(nil), retired[len(retired)-model.MaxRetiredEpochs:]...)
	}
	state.RetiredEpochs = retired
}

func unretireEpoch(state *model.BindingState, epoch string) {
	if epoch == "" || !slices.Contains(state.RetiredEpochs, epoch) {
		return
	}
	state.RetiredEpochs = slices.DeleteFunc(append([]string(nil), state.RetiredEpochs...), func(e string) bool { return e == epoch })
}

// orderVerdict compares a not-yet-accepted identity with the cursor. Time
// equality with a different sample is ambiguous and is a conflict, never an
// overwrite. A sequence or epoch is authoritative within its own mode.
func orderVerdict(cursor model.ReportCursor, mode string, sampledAt time.Time, r model.ExternalReport) (Verdict, string) {
	if cursor.Identity == "" {
		return VerdictNew, ""
	}
	if mode == "sequence" && cursor.Mode == "sequence" {
		if r.Epoch == cursor.Epoch {
			if cursor.Sequence == nil || r.Sequence == nil {
				return VerdictConflict, "sequence_missing"
			}
			switch {
			case *r.Sequence < *cursor.Sequence:
				return VerdictLate, "older_sequence"
			case *r.Sequence > *cursor.Sequence:
				return VerdictNew, ""
			default:
				return VerdictConflict, "same_sequence_different_content"
			}
		}
	}
	switch {
	case sampledAt.Before(cursor.ObservedAt):
		return VerdictLate, "older_timestamp"
	case sampledAt.Equal(cursor.ObservedAt):
		return VerdictConflict, "ambiguous_same_time"
	default:
		return VerdictNew, ""
	}
}

func addCount(n int64) int64 {
	if n >= math.MaxInt64 {
		return math.MaxInt64
	}
	return n + 1
}

func cloneUint(v *uint64) *uint64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

// MergeSource applies one collection attempt to source contact state. A newer
// successful read advances contact; a failure updates attempt diagnostics but
// cannot move LastContact. Older attempts are ignored, preserving replay.
func MergeSource(state model.SourceState, source string, attemptedAt time.Time, contact bool, code string, durationMS int64) model.SourceState {
	if source == "" || attemptedAt.IsZero() {
		return state
	}
	if state.ID == "" {
		state.ID = source
	}
	if !state.LastAttemptAt.IsZero() && !attemptedAt.After(state.LastAttemptAt) {
		return state
	}
	state.LastAttemptAt = attemptedAt
	state.LastCode = code
	state.LastDurationMS = durationMS
	if contact {
		state.LastResult = "ok"
		if attemptedAt.After(state.LastContact) {
			state.LastContact = attemptedAt
		}
	} else {
		state.LastResult = "error"
	}
	return state
}
