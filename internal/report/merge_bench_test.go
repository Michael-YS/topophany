package report_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

func BenchmarkMergeBindingDuplicate256Seen(b *testing.B) {
	at := time.Now().UTC()
	binding := model.ExternalBinding{Source: "s", Object: "vm", Report: model.ReportTypeResource, MaxAgeSeconds: 60}
	var state model.BindingState
	for i := 0; i < model.MaxSourceReportIDs; i++ {
		r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm", SampleID: fmt.Sprintf("sample-%03d", i), ObservedAt: at.Add(time.Duration(i) * time.Second), TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: floatPointer(1)}}}
		state, _ = report.MergeBinding("check", "contract", binding, state, r, at)
	}
	duplicate := state.Last
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		got, verdict := report.MergeBinding("check", "contract", binding, state, *duplicate, at)
		if verdict.Verdict != report.VerdictDuplicate || got.ReportSamples != model.MaxSourceReportIDs {
			b.Fatal(verdict, got.ReportSamples)
		}
	}
}

func floatPointer(v float64) *float64 { return &v }
