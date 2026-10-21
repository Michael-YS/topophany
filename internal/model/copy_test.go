package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCloneBatchOwnership(t *testing.T) {
	percent := 25.0
	original := Batch{Schema: BatchSchema, Project: "project", Revision: "revision",
		Observations: []Observation{{ID: "observer.boot.1", SampledAt: time.Now(), Facts: map[string]string{"proof": "yes"}, Timings: map[string]int64{"connect": 12},
			External: &ExternalObservation{Source: "source", AttemptedAt: time.Now(), Contact: true, Report: &ExternalReport{ReportMeta: ReportMeta{Schema: ReportSchema, Object: "vm", TimeBasis: TimeBasisUpstreamSample, Fields: map[string]string{"cpu.percent": FieldKnown}}, Resource: &ResourceReport{CPU: &CPUReport{Percent: &percent}}}}}},
		Heartbeat: &Heartbeat{Observer: "observer", Delivery: []DeliveryStatus{{Collector: "0123456789abcdef"}}},
	}
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	cloned := CloneBatch(original)
	after, err := json.Marshal(cloned)
	if err != nil || string(before) != string(after) {
		t.Fatalf("clone changes wire representation: %s %v", after, err)
	}
	cloned.Observations[0].ID = "changed"
	cloned.Observations[0].Facts["proof"] = "changed"
	cloned.Observations[0].Timings["connect"] = 99
	cloned.Observations[0].External.Report.Resource.CPU.Percent = ptr(99.0)
	cloned.Observations[0].External.Report.Fields["cpu.percent"] = "changed"
	cloned.Heartbeat.Observer = "changed"
	cloned.Heartbeat.Delivery[0].Collector = "changed"
	unchanged, _ := json.Marshal(original)
	if string(before) != string(unchanged) {
		t.Fatal("clone aliases input")
	}
	original.Observations[0].Facts["proof"] = "input changed"
	original.Heartbeat.Delivery[0].Collector = "input changed"
	if cloned.Observations[0].Facts["proof"] != "changed" || cloned.Heartbeat.Delivery[0].Collector != "changed" || *cloned.Observations[0].External.Report.Resource.CPU.Percent != 99 {
		t.Fatal("input aliases clone")
	}
	if *original.Observations[0].External.Report.Resource.CPU.Percent != 25 || original.Observations[0].External.Report.Fields["cpu.percent"] != FieldKnown {
		t.Fatal("clone aliases external report")
	}
}

func TestCloneBatchPreservesEmptyAndNil(t *testing.T) {
	for _, b := range []Batch{{}, {Observations: []Observation{}}, {Observations: []Observation{{Facts: map[string]string{}, Timings: map[string]int64{}}}, Heartbeat: &Heartbeat{Delivery: []DeliveryStatus{}}}} {
		if got := CloneBatch(b); !reflect.DeepEqual(b, got) {
			t.Fatalf("clone %+v != %+v", got, b)
		}
	}
}

func BenchmarkCloneBatch(b *testing.B) {
	batch := Batch{Heartbeat: &Heartbeat{Delivery: []DeliveryStatus{{Collector: "0123456789abcdef"}}}}
	for range 100 {
		batch.Observations = append(batch.Observations, Observation{Facts: map[string]string{"proof": "response", "colo": "SJC"}, Timings: map[string]int64{"connect": 12}})
	}
	b.Run("explicit", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			CloneBatch(batch)
		}
	})
	b.Run("json-roundtrip", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			raw, _ := json.Marshal(batch)
			var owned Batch
			if err := json.Unmarshal(raw, &owned); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func ptr(v float64) *float64 { return &v }
