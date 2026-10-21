package report_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

func mergeFixture() (model.ExternalBinding, model.BindingState, model.ExternalReport, time.Time) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	b := model.ExternalBinding{Source: "source", Object: "vm/one", Report: model.ReportTypeStatus, MaxAgeSeconds: 60}
	r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/one", SampleID: "s1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: "up"}}
	return b, model.BindingState{}, r, at
}

func TestMergeNewDuplicateSameValueAndDeadline(t *testing.T) {
	b, state, r, at := mergeFixture()
	state, got := report.MergeBinding("check", "contract", b, state, r, at)
	if got.Verdict != report.VerdictNew || state.ReportSamples != 1 || state.UpRun != 1 || !state.DataDeadline.Equal(at.Add(time.Minute)) {
		t.Fatalf("new = %+v %+v", got, state)
	}
	deadline, accepted, up := state.DataDeadline, state.LastAcceptedAt, state.UpRun
	// A replayed observation with a new receive time but the same upstream
	// sample is a duplicate: no deadline, confirmation or latency movement.
	state, got = report.MergeBinding("check", "contract", b, state, r, at.Add(10*time.Second))
	if got.Verdict != report.VerdictDuplicate || !state.DataDeadline.Equal(deadline) || !state.LastAcceptedAt.Equal(accepted) || state.UpRun != up || state.ReportSamples != 1 {
		t.Fatalf("duplicate advanced state: %+v %+v", got, state)
	}
	if state.Duplicates != 1 || state.LastResult != "duplicate" {
		t.Fatalf("duplicate diagnostics = %+v", state)
	}
	// A new identity with the same values is still a new sample.
	r.SampleID = "s2"
	r.ObservedAt = at.Add(time.Second)
	state, got = report.MergeBinding("check", "contract", b, state, r, at.Add(11*time.Second))
	if got.Verdict != report.VerdictNew || state.ReportSamples != 2 || state.UpRun != 2 || !state.DataDeadline.Equal(at.Add(time.Minute+time.Second)) {
		t.Fatalf("same-value new sample = %+v %+v", got, state)
	}
}

func TestMergeConflictLateUnusable(t *testing.T) {
	b, state, r, at := mergeFixture()
	state, _ = report.MergeBinding("check", "contract", b, state, r, at)
	late := r
	late.SampleID = "s0"
	late.ObservedAt = at.Add(-time.Second)
	state, got := report.MergeBinding("check", "contract", b, state, late, at)
	if got.Verdict != report.VerdictLate || state.Late != 1 || state.ReportSamples != 1 || !state.DataDeadline.Equal(at.Add(time.Minute)) {
		t.Fatalf("late = %+v %+v", got, state)
	}
	conflict := r
	conflict.Status = &model.StatusReport{Status: "down"}
	state, got = report.MergeBinding("check", "contract", b, state, conflict, at)
	if got.Verdict != report.VerdictConflict || state.Conflicts != 1 || state.ReportSamples != 1 || state.StatusDown != 0 {
		t.Fatalf("conflict = %+v %+v", got, state)
	}
	// A different sample ID at the same timestamp is not a licence to
	// overwrite the current report.
	sameTime := r
	sameTime.SampleID = "s-other"
	state, got = report.MergeBinding("check", "contract", b, state, sameTime, at)
	if got.Verdict != report.VerdictConflict || state.ReportSamples != 1 {
		t.Fatalf("same-time conflict = %+v %+v", got, state)
	}
	unknown := r
	unknown.SampleID = "unknown"
	unknown.TimeBasis = model.TimeBasisUnknown
	unknown.ObservedAt = time.Time{}
	state, got = report.MergeBinding("check", "contract", b, state, unknown, at)
	if got.Verdict != report.VerdictUnusable || state.Unusable != 1 || state.ReportSamples != 1 {
		t.Fatalf("unusable = %+v %+v", got, state)
	}
}

func TestMergeSequenceEpochOrderingAndBasisReset(t *testing.T) {
	b := model.ExternalBinding{Source: "source", Object: "vm/one", Report: model.ReportTypeStatus, MaxAgeSeconds: 60}
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	seq := func(n uint64) *uint64 { return &n }
	r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/one", Epoch: "boot-a", Sequence: seq(10), ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: "down"}}
	state := model.BindingState{}
	state, got := report.MergeBinding("check", "contract", b, state, r, at)
	if got.Verdict != report.VerdictNew || state.DownRun != 1 {
		t.Fatalf("sequence new = %+v %+v", got, state)
	}
	older := r
	older.Sequence = seq(9)
	state, got = report.MergeBinding("check", "contract", b, state, older, at)
	if got.Verdict != report.VerdictLate {
		t.Fatalf("older sequence = %+v", got)
	}
	// A new epoch with a later time starts a new series, but confirmation
	// runs reset rather than inheriting a clean or failing run.
	nextEpoch := r
	nextEpoch.Epoch = "boot-b"
	nextEpoch.Sequence = seq(1)
	nextEpoch.ObservedAt = at.Add(30 * time.Second)
	nextEpoch.Status = &model.StatusReport{Status: "up"}
	state, got = report.MergeBinding("check", "contract", b, state, nextEpoch, at.Add(30*time.Second))
	if got.Verdict != report.VerdictNew || state.UpRun != 1 || state.DownRun != 0 {
		t.Fatalf("new epoch = %+v %+v", got, state)
	}
	// A different basis resets runs. The report is still new and accepted.
	oldBasis := nextEpoch
	oldBasis.SampleID = "basis-2"
	oldBasis.ObservedAt = at.Add(time.Minute)
	oldBasis.Status = &model.StatusReport{Status: "up"}
	state, got = report.MergeBinding("check", "contract", b, state, oldBasis, at.Add(time.Minute))
	if got.BasisChanged || state.UpRun != 2 {
		t.Fatalf("first basis = %+v %+v", got, state)
	}
	state.Basis = "manual-old"
	changed := oldBasis
	changed.SampleID = "basis-3"
	changed.ObservedAt = at.Add(90 * time.Second)
	state, got = report.MergeBinding("check", "contract", b, state, changed, at.Add(90*time.Second))
	if !got.BasisChanged || state.UpRun != 1 {
		t.Fatalf("basis reset = %+v %+v", got, state)
	}
}

func TestMergePolicyStatsAndLatencyAreNewSamplesOnly(t *testing.T) {
	b := model.ExternalBinding{Source: "source", Object: "vm/one", Report: model.ReportTypeResource, MaxAgeSeconds: 60, PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: f(80)}}
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cpu := 90.0
	r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/one", SampleID: "r1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu}}}
	state := model.BindingState{}
	state, got := report.MergeBinding("check", "contract", b, state, r, at)
	if got.Verdict != report.VerdictNew || state.PolicyRun != 1 || state.PolicyViolations != 1 || state.ResourceSamples != 1 {
		t.Fatalf("policy new = %+v %+v", got, state)
	}
	state, _ = report.MergeBinding("check", "contract", b, state, r, at.Add(time.Second))
	if state.PolicyRun != 1 || state.PolicyViolations != 1 || state.ResourceSamples != 1 {
		t.Fatalf("duplicate advanced policy stats: %+v", state)
	}
	latency := int64(42)
	statusBinding := model.ExternalBinding{Source: "source", Object: "svc", Report: model.ReportTypeStatus, MaxAgeSeconds: 60}
	sr := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "svc", SampleID: "st1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: "up", UpstreamLatencyMS: &latency}}
	state = model.BindingState{}
	state, _ = report.MergeBinding("status", "contract", statusBinding, state, sr, at)
	state, _ = report.MergeBinding("status", "contract", statusBinding, state, sr, at.Add(time.Second))
	if state.UpstreamLatencySamples != 1 || state.UpstreamLatencySumMS != 42 || state.StatusUp != 1 || state.ReportSamples != 1 {
		t.Fatalf("latency duplicate counted: %+v", state)
	}
	sr.SampleID = "st2"
	sr.Status.UpstreamLatencyMS = nil
	sr.ObservedAt = at.Add(time.Minute)
	state, _ = report.MergeBinding("status", "contract", statusBinding, state, sr, at.Add(time.Minute))
	if state.UpstreamLatencySamples != 1 || state.UpstreamLatencySumMS != 42 {
		t.Fatal("missing latency was treated as zero")
	}
}

func TestMergeSourceContactAndFailures(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	state := report.MergeSource(model.SourceState{}, "source", at, true, "", 5)
	if state.LastContact != at || state.LastResult != "ok" {
		t.Fatalf("contact = %+v", state)
	}
	state = report.MergeSource(state, "source", at.Add(time.Minute), false, "timeout", 2000)
	if !state.LastContact.Equal(at) || state.LastResult != "error" || state.LastCode != "timeout" {
		t.Fatalf("failure moved contact or lost diagnostics: %+v", state)
	}
	// An older successful replay cannot move contact.
	state = report.MergeSource(state, "source", at, true, "", 5)
	if !state.LastContact.Equal(at) || state.LastResult != "error" {
		t.Fatalf("older replay changed contact: %+v", state)
	}
	state = report.MergeSource(state, "source", at.Add(2*time.Minute), true, "", 7)
	if !state.LastContact.Equal(at.Add(2*time.Minute)) || state.LastResult != "ok" || state.LastAttemptAt != at.Add(2*time.Minute) {
		t.Fatalf("new contact = %+v", state)
	}
}

func TestMergeStatusConfirmationOnlyFromNewValidReports(t *testing.T) {
	b := model.ExternalBinding{Source: "source", Object: "svc", Report: model.ReportTypeStatus, MaxAgeSeconds: 60}
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	state := model.BindingState{Check: "check", Contract: "contract", Source: b.Source, Object: b.Object, ReportType: b.Report, MaxAgeSeconds: b.MaxAgeSeconds, FailureThreshold: 2, RecoveryThreshold: 2}
	down := func(id string, at time.Time) model.ExternalReport {
		return model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "svc", SampleID: id, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: "down"}}
	}
	state, _ = report.MergeBinding("check", "contract", b, state, down("d1", at), at)
	if state.ConfirmedDown || state.DownRun != 1 {
		t.Fatalf("confirmed too early: %+v", state)
	}
	state, _ = report.MergeBinding("check", "contract", b, state, down("d2", at.Add(time.Second)), at.Add(time.Second))
	if !state.ConfirmedDown || state.DownRun != 2 {
		t.Fatalf("not confirmed at threshold: %+v", state)
	}
	state, _ = report.MergeBinding("check", "contract", b, state, down("d2", at.Add(time.Second)), at.Add(2*time.Second))
	if !state.ConfirmedDown || state.DownRun != 2 || state.Duplicates != 1 {
		t.Fatalf("duplicate advanced confirmation: %+v", state)
	}
	up := func(id string, at time.Time) model.ExternalReport {
		return model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "svc", SampleID: id, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: "up"}}
	}
	state, _ = report.MergeBinding("check", "contract", b, state, up("u1", at.Add(2*time.Second)), at.Add(2*time.Second))
	if !state.ConfirmedDown || state.UpRun != 1 || state.DownRun != 0 {
		t.Fatalf("recovered too early: %+v", state)
	}
	state, _ = report.MergeBinding("check", "contract", b, state, up("u2", at.Add(3*time.Second)), at.Add(3*time.Second))
	if state.ConfirmedDown || state.UpRun != 2 {
		t.Fatalf("recovery did not confirm: %+v", state)
	}
}

func acceptedState(s model.BindingState) model.BindingState {
	s = model.CloneBindingState(s)
	s.Duplicates, s.Late, s.Conflicts, s.Unusable = 0, 0, 0, 0
	s.LastResult, s.LastCode = "", ""
	return s
}

func TestMergeRejectedVerdictsPreserveAcceptedState(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cpu := 90.0
	binding := model.ExternalBinding{Source: "source", Object: "vm/one", Report: model.ReportTypeResource, MaxAgeSeconds: 60, Assertions: &model.ResourceAssertions{CPUPercentMax: f(80)}}
	base := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/one", SampleID: "p1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu, Basis: "original"}}}
	initial := model.BindingState{PolicyThreshold: 2}
	state, got := report.MergeBinding("check", "contract", binding, initial, base, at)
	if got.Verdict != report.VerdictNew || state.PolicyRun != 1 || state.PolicyConfirmed {
		t.Fatalf("first accepted policy report = %+v %+v", got, state)
	}
	second := base
	second.SampleID = "p2"
	second.ObservedAt = at.Add(time.Second)
	state, _ = report.MergeBinding("check", "contract", binding, state, second, at.Add(time.Second))
	if state.PolicyRun != 2 || !state.PolicyConfirmed {
		t.Fatalf("policy did not confirm: %+v", state)
	}

	makeConflict := func() model.ExternalReport {
		r := model.CloneExternalReport(base)
		r.Resource.CPU.Basis = "different"
		return r
	}
	makeLate := func() model.ExternalReport {
		r := model.CloneExternalReport(base)
		r.SampleID = "p0"
		r.ObservedAt = at.Add(-time.Second)
		r.Resource.CPU.Basis = "different"
		return r
	}
	makeUnusable := func() model.ExternalReport {
		r := model.CloneExternalReport(base)
		r.TimeBasis = model.TimeBasisUnknown
		r.ObservedAt = time.Time{}
		r.Resource.CPU.Basis = "different"
		return r
	}
	for name, tc := range map[string]struct {
		r       model.ExternalReport
		verdict report.Verdict
	}{
		"duplicate": {base, report.VerdictDuplicate},
		"conflict":  {makeConflict(), report.VerdictConflict},
		"late":      {makeLate(), report.VerdictLate},
		"unusable":  {makeUnusable(), report.VerdictUnusable},
	} {
		t.Run(name, func(t *testing.T) {
			before := model.CloneBindingState(state)
			input := model.CloneBindingState(state)
			after, result := report.MergeBinding("check", "contract", binding, input, tc.r, at.Add(2*time.Second))
			if result.Verdict != tc.verdict {
				t.Fatalf("verdict = %+v", result)
			}
			if result.BasisChanged {
				t.Fatal("rejected report claimed an accepted basis change")
			}
			if !reflect.DeepEqual(input, before) {
				t.Fatal("MergeBinding mutated its input state")
			}
			if !reflect.DeepEqual(acceptedState(after), acceptedState(before)) {
				t.Fatalf("rejected %s changed accepted state:\nbefore=%+v\nafter=%+v", name, before, after)
			}
			if after.LastResult != string(tc.verdict) {
				t.Fatalf("diagnostic result = %q, want %q", after.LastResult, tc.verdict)
			}
		})
	}

	// A new, valid report under a different basis is the only path that may
	// reset the runs; the first sample of the new basis is then counted.
	changed := model.CloneExternalReport(base)
	changed.SampleID = "p3"
	changed.ObservedAt = at.Add(2 * time.Second)
	changed.Resource.CPU.Basis = "different"
	after, result := report.MergeBinding("check", "contract", binding, state, changed, at.Add(2*time.Second))
	if result.Verdict != report.VerdictNew || !result.BasisChanged {
		t.Fatalf("valid basis change = %+v %+v", result, after)
	}
	if after.PolicyRun != 1 || after.PolicyConfirmed || after.ResourceSamples != 3 || after.ReportSamples != 3 {
		t.Fatalf("basis reset did not re-count the first new sample: %+v", after)
	}
}

func statusSample(epoch string, sequence uint64, status string, at time.Time) model.ExternalReport {
	seq := sequence
	return model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "svc", Epoch: epoch, Sequence: &seq, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample},
		Status:     &model.StatusReport{Status: status},
	}
}

func TestMergeNewEpochResetsFailureAndRecoveryConfirmation(t *testing.T) {
	b := model.ExternalBinding{Source: "source", Object: "svc", Report: model.ReportTypeStatus, MaxAgeSeconds: 60}
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	state := model.BindingState{FailureThreshold: 2, RecoveryThreshold: 2}

	// A down -> confirmed down under epoch A.
	for i, offset := range []time.Duration{0, 10 * time.Second} {
		state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", uint64(i+1), "down", at.Add(offset)), at.Add(offset))
	}
	if !state.ConfirmedDown || state.DownRun != 2 {
		t.Fatalf("epoch A did not confirm: %+v", state)
	}
	// The same outcome in a new epoch must not inherit the old run.
	newEpoch := statusSample("boot-b", 1, "down", at.Add(20*time.Second))
	state, got := report.MergeBinding("check", "contract", b, state, newEpoch, at.Add(20*time.Second))
	if got.Verdict != report.VerdictNew || state.DownRun != 1 || state.ConfirmedDown {
		t.Fatalf("new epoch inherited failure confirmation: %+v %+v", got, state)
	}
	if len(state.RetiredEpochs) != 1 || state.RetiredEpochs[0] != "boot-a" {
		t.Fatalf("retired epochs = %+v", state.RetiredEpochs)
	}
	if state.ReportSamples != 3 || state.StatusDown != 3 {
		t.Fatalf("historical counts were rewound: %+v", state)
	}
	before := model.CloneBindingState(state)
	old := statusSample("boot-a", 3, "down", at.Add(30*time.Second))
	after, got := report.MergeBinding("check", "contract", b, state, old, at.Add(30*time.Second))
	if got.Verdict != report.VerdictLate || got.Code != "retired_epoch" {
		t.Fatalf("old epoch was not refused: %+v", got)
	}
	if !reflect.DeepEqual(acceptedState(after), acceptedState(before)) {
		t.Fatal("old epoch replay changed accepted state")
	}
	// An up in the new epoch starts its own recovery count.
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-b", 2, "up", at.Add(40*time.Second)), at.Add(40*time.Second))
	if state.UpRun != 1 || state.ConfirmedDown {
		t.Fatalf("new epoch inherited recovery confirmation: %+v", state)
	}

	// A fresh recovery lifecycle: confirmed down, one up under epoch C.
	state = model.BindingState{FailureThreshold: 2, RecoveryThreshold: 2}
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-c", 1, "down", at), at)
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-c", 2, "down", at.Add(10*time.Second)), at.Add(10*time.Second))
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-c", 3, "up", at.Add(20*time.Second)), at.Add(20*time.Second))
	if !state.ConfirmedDown || state.UpRun != 1 {
		t.Fatalf("recovery setup = %+v", state)
	}
	state, got = report.MergeBinding("check", "contract", b, state, statusSample("boot-d", 1, "up", at.Add(30*time.Second)), at.Add(30*time.Second))
	if got.Verdict != report.VerdictNew || state.UpRun != 1 || state.ConfirmedDown {
		t.Fatalf("new epoch inherited a recovery run: %+v %+v", got, state)
	}
}

func TestMergeEvidenceGapRestartsConfirmationWithoutFakeRecovery(t *testing.T) {
	b := model.ExternalBinding{Source: "source", Object: "svc", Report: model.ReportTypeStatus, MaxAgeSeconds: 60}
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	state := model.BindingState{FailureThreshold: 2, RecoveryThreshold: 2}
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 1, "down", at), at)
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 2, "down", at.Add(2*time.Minute)), at.Add(2*time.Minute))
	if state.DownRun != 1 || state.ConfirmedDown {
		t.Fatalf("hour-gap failure counted as consecutive: %+v", state)
	}
	if state.ReportSamples != 2 || state.StatusDown != 2 {
		t.Fatalf("historical counts after gap = %+v", state)
	}

	// Exactly at the exclusive data deadline is already a gap.
	state = model.BindingState{FailureThreshold: 2}
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 1, "down", at), at)
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 2, "down", at.Add(60*time.Second)), at.Add(60*time.Second))
	if state.DownRun != 1 || state.ConfirmedDown {
		t.Fatalf("deadline boundary counted as continuous: %+v", state)
	}
	// Just inside the window remains continuous.
	state = model.BindingState{FailureThreshold: 2}
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 1, "down", at), at)
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 2, "down", at.Add(59*time.Second)), at.Add(59*time.Second))
	if state.DownRun != 2 || !state.ConfirmedDown {
		t.Fatalf("continuous sample was split: %+v", state)
	}

	// A confirmed outage stays confirmed across a gap until recovery is
	// itself confirmed from new reports; one up sample cannot fake it.
	state = model.BindingState{FailureThreshold: 2, RecoveryThreshold: 2}
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 1, "down", at), at)
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 2, "down", at.Add(10*time.Second)), at.Add(10*time.Second))
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 3, "up", at.Add(20*time.Second)), at.Add(20*time.Second))
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 4, "up", at.Add(3*time.Minute)), at.Add(3*time.Minute))
	if !state.ConfirmedDown || state.UpRun != 1 {
		t.Fatalf("gap forged recovery: %+v", state)
	}
	state, _ = report.MergeBinding("check", "contract", b, state, statusSample("boot-a", 5, "up", at.Add(3*time.Minute+10*time.Second)), at.Add(3*time.Minute+10*time.Second))
	if state.ConfirmedDown || state.UpRun != 2 {
		t.Fatalf("confirmed recovery did not complete: %+v", state)
	}
}

func TestMergePolicyGapAndEpochLifecycle(t *testing.T) {
	b := model.ExternalBinding{Source: "source", Object: "vm/one", Report: model.ReportTypeResource, MaxAgeSeconds: 60, Assertions: &model.ResourceAssertions{CPUPercentMax: f(80)}}
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cpu := 90.0
	reportAt := func(epoch string, seq uint64, at time.Time) model.ExternalReport {
		n := seq
		return model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/one", Epoch: epoch, Sequence: &n, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu, Basis: "original"}}}
	}
	state := model.BindingState{PolicyThreshold: 2}
	state, _ = report.MergeBinding("check", "contract", b, state, reportAt("boot-a", 1, at), at)
	state, _ = report.MergeBinding("check", "contract", b, state, reportAt("boot-a", 2, at.Add(10*time.Second)), at.Add(10*time.Second))
	if !state.PolicyConfirmed || state.PolicyRun != 2 {
		t.Fatalf("policy did not confirm: %+v", state)
	}
	state, got := report.MergeBinding("check", "contract", b, state, reportAt("boot-b", 1, at.Add(20*time.Second)), at.Add(20*time.Second))
	if got.Verdict != report.VerdictNew || state.PolicyRun != 1 || state.PolicyConfirmed {
		t.Fatalf("new epoch inherited policy confirmation: %+v %+v", got, state)
	}
	if state.PolicyViolations != 3 {
		t.Fatalf("policy history was rewound: %+v", state)
	}
	old := reportAt("boot-a", 3, at.Add(30*time.Second))
	before := model.CloneBindingState(state)
	after, got := report.MergeBinding("check", "contract", b, state, old, at.Add(30*time.Second))
	if got.Verdict != report.VerdictLate || got.Code != "retired_epoch" || !reflect.DeepEqual(acceptedState(after), acceptedState(before)) {
		t.Fatalf("old policy epoch = %+v; accepted state changed", got)
	}

	state = model.BindingState{PolicyThreshold: 2}
	state, _ = report.MergeBinding("check", "contract", b, state, reportAt("boot-a", 1, at), at)
	state, _ = report.MergeBinding("check", "contract", b, state, reportAt("boot-a", 2, at.Add(2*time.Minute)), at.Add(2*time.Minute))
	if state.PolicyRun != 1 || state.PolicyConfirmed {
		t.Fatalf("policy gap counted as continuous: %+v", state)
	}
}

func TestMergeDuplicateReplayIsIdempotent(t *testing.T) {
	b, state, r, at := mergeFixture()
	state, _ = report.MergeBinding("check", "contract", b, state, r, at)
	before := acceptedState(state)
	basis, cursor, last := state.Basis, state.Cursor, state.Last
	for i := 0; i < 50; i++ {
		next, result := report.MergeBinding("check", "contract", b, state, r, at.Add(time.Duration(i+1)*time.Second))
		if result.Verdict != report.VerdictDuplicate {
			t.Fatalf("replay %d verdict = %+v", i, result)
		}
		state = next
	}
	if !reflect.DeepEqual(acceptedState(state), before) {
		t.Fatalf("duplicate replay changed accepted state:\nbefore=%+v\nafter=%+v", before, state)
	}
	if state.Basis != basis || !reflect.DeepEqual(state.Cursor, cursor) || !reflect.DeepEqual(state.Last, last) {
		t.Fatal("duplicate replay changed basis, cursor or last report")
	}
	if state.Duplicates != 50 || state.ReportSamples != 1 {
		t.Fatalf("duplicate diagnostics = %+v", state)
	}
}
