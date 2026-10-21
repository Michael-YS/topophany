package admission_test

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

func fixture(now time.Time) (model.Observation, model.Check, model.Source) {
	percent := 25.0
	at := now.Add(-time.Minute)
	r := model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/one", SampleID: "s1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample},
		Resource:   &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}},
	}
	v := model.Observation{
		ID: "home.boot.1", Project: "lab", Contract: "contract", Check: "vm-resources", Observer: "home", Boot: "boot", Sequence: 1,
		SampledAt: now, Outcome: "ok", Evidence: "reported", Stage: "external_source", DurationMS: 5,
		External: &model.ExternalObservation{Source: "source-a", SourceGeneration: "gen-1", AttemptedAt: now, Contact: true, Report: &r},
	}
	binding := model.ExternalBinding{Source: "source-a", Object: "vm/one", Report: model.ReportTypeResource}
	x := model.Check{ID: "vm-resources", Observer: "home", Kind: "external", External: &binding, TimeoutSeconds: 5}
	s := model.Source{ID: "source-a", Observer: "home", Adapter: "synthetic", Target: "https://source.invalid", MaxReportBytes: 64 << 10}
	return v, x, s
}

func window(now time.Time) admission.Window {
	return admission.Window{Now: now, Future: now, MaxAge: 24 * time.Hour, Skew: 30 * time.Second}
}

func TestExternalAdmissionAcceptsLiveReportAndOwnsIt(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	v, x, s := fixture(now)
	r, err := admission.External(v, x, s, window(now))
	if err != nil || r == nil || r.Resource == nil || r.Resource.CPU.Percent == nil {
		t.Fatalf("valid report refused: %v", err)
	}
	// The returned report is detached from the observation.
	*r.Resource.CPU.Percent = 99
	if *v.External.Report.Resource.CPU.Percent != 25 {
		t.Fatal("admission returned an aliased report")
	}
}

func TestExternalAdmissionTimeRelations(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		mutate func(*model.Observation, *model.Check)
		code   string
	}{
		"future envelope": {func(v *model.Observation, _ *model.Check) {
			v.SampledAt = now.Add(24 * time.Hour)
		}, "sample_time_out_of_range"},
		"future attempt": {func(v *model.Observation, _ *model.Check) {
			v.External.AttemptedAt = now.Add(2 * time.Hour)
		}, "external_attempt_time_out_of_range"},
		"old attempt": {func(v *model.Observation, _ *model.Check) {
			v.External.AttemptedAt = now.Add(-48 * time.Hour)
			v.External.Report.ObservedAt = now.Add(-49 * time.Hour)
		}, "external_attempt_time_out_of_range"},
		"future report": {func(v *model.Observation, _ *model.Check) {
			v.External.Report.ObservedAt = now.Add(2 * time.Hour)
		}, "external_report_time_out_of_range"},
		"report after read": {func(v *model.Observation, _ *model.Check) {
			v.External.Report.ObservedAt = now.Add(time.Minute)
		}, "external_report_time_out_of_range"},
		"live read before collection": {func(v *model.Observation, _ *model.Check) {
			v.External.Report.TimeBasis = model.TimeBasisLiveRead
			v.External.Report.ObservedAt = now.Add(-2 * time.Minute)
		}, "external_report_time_out_of_range"},
		"unknown time is a shaped attempt": {func(v *model.Observation, _ *model.Check) {
			v.External.Report.TimeBasis = model.TimeBasisUnknown
			v.External.Report.ObservedAt = time.Time{}
		}, ""},
		"missing attempt": {func(v *model.Observation, _ *model.Check) {
			v.External.AttemptedAt = time.Time{}
		}, "external_attempt_time_missing"},
		"clock skew inside allowance": {func(v *model.Observation, _ *model.Check) {
			v.External.AttemptedAt = now.Add(20 * time.Second)
			v.External.Report.ObservedAt = now.Add(-time.Minute)
		}, ""},
		"collector rollback during read": {func(v *model.Observation, _ *model.Check) {
			v.External.AttemptedAt = now.Add(-time.Minute)
			v.External.Report.ObservedAt = now.Add(-10 * time.Second)
		}, ""},
		"historical report is stored": {func(v *model.Observation, _ *model.Check) {
			v.External.Report.ObservedAt = now.Add(-48 * time.Hour)
		}, ""},
		"pre-epoch report": {func(v *model.Observation, _ *model.Check) {
			v.External.Report.ObservedAt = time.Date(1969, 12, 31, 23, 59, 0, 0, time.UTC)
		}, "external_report_time_out_of_range"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v, x, s := fixture(now)
			tc.mutate(&v, &x)
			_, err := admission.External(v, x, s, window(now))
			if tc.code == "" {
				if err != nil {
					t.Fatalf("valid live observation refused: %v", err)
				}
				return
			}
			if err == nil || admission.Code(err) != tc.code {
				t.Fatalf("code = %q, want %q (%v)", admission.Code(err), tc.code, err)
			}
		})
	}
}

func TestExternalAdmissionReusesReportAndBoundChecks(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	v, x, s := fixture(now)
	v.External.Report.Resource.CPU.Percent = nil
	v.External.Report.Resource = &model.ResourceReport{}
	if _, err := admission.External(v, x, s, window(now)); err == nil || admission.Code(err) != "empty_resource_report" {
		t.Fatalf("empty payload accepted: %v", err)
	}
	v, x, s = fixture(now)
	v.External.Report.Fields = map[string]string{"disk.root.percent": model.FieldMissing}
	if _, err := admission.External(v, x, s, window(now)); err == nil || admission.Code(err) != "unknown_field" {
		t.Fatalf("foreign field accepted: %v", err)
	}
	v, x, s = fixture(now)
	v.DurationMS = 10_001
	if _, err := admission.External(v, x, s, window(now)); err == nil || admission.Code(err) != "observation_budget_exceeded" {
		t.Fatalf("overlong attempt accepted: %v", err)
	}
	if _, err := admission.External(v, x, s, admission.Window{}); err == nil || admission.Code(err) != "admission_window_invalid" {
		t.Fatalf("invalid window accepted: %v", err)
	}
	if got := admission.Code(&report.Error{Code: "wrapped_refusal"}); got != "wrapped_refusal" {
		t.Fatalf("report refusal code lost: %q", got)
	}
}
