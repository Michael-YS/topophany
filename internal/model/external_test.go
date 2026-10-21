package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestExternalReportPayloadsAreExclusiveAndNullable(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	seq := uint64(0)
	zero := 0.0
	r := model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm-1", Epoch: "boot", Sequence: &seq, ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample, Fields: map[string]string{"cpu.percent": model.FieldKnown, "memory.total_bytes": model.FieldMissing}},
		Resource:   &model.ResourceReport{CPU: &model.CPUReport{Percent: &zero}, Memory: &model.MemoryReport{}, Disks: []model.DiskReport{{ID: "root"}}},
	}
	if r.PayloadCount() != 1 {
		t.Fatalf("resource report counted %d payloads", r.PayloadCount())
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back model.ExternalReport
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Resource == nil || back.Resource.CPU == nil || back.Resource.CPU.Percent == nil || *back.Resource.CPU.Percent != 0 {
		t.Fatal("explicit zero percent was lost")
	}
	if back.Resource.Memory.TotalBytes != nil {
		t.Fatal("absent bytes became a number")
	}
	if back.Sequence == nil || *back.Sequence != 0 {
		t.Fatal("explicit zero sequence was lost")
	}
	if back.PayloadCount() != 1 {
		t.Fatal("report did not roundtrip with one payload")
	}
	r.Status = &model.StatusReport{Status: "up"}
	if r.PayloadCount() != 2 {
		t.Fatal("two payloads were not counted")
	}
	empty := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema}}
	if empty.PayloadCount() != 0 {
		t.Fatal("empty report counted a payload")
	}
}

func TestReportTimeBasisVocabulary(t *testing.T) {
	for _, v := range []string{model.TimeBasisUpstreamSample, model.TimeBasisLiveRead, model.TimeBasisUnknown} {
		if v == "" {
			t.Fatal("empty time basis")
		}
	}
}
