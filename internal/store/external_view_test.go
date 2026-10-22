package store

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestEvaluationViewOwnsExternalAttemptsAndReports(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, externalStoreOptions(c, &now))
	for sequence := uint64(1); sequence <= 2; sequence++ {
		at := now.Add(time.Duration(int64(sequence)-2) * time.Second)
		v := externalStoreObservation(t, c, "sample-"+testkitSeq(sequence), 20, sequence, at, at)
		reportSequence := sequence
		v.External.Report.Sequence = &reportSequence
		v.External.Report.Epoch = "epoch-1"
		v.External.Report.Fields = map[string]string{"cpu.percent": model.FieldKnown}
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.rows) != 1 {
		t.Fatalf("fixture must trim the older row from raw history: %d", len(s.rows))
	}
	want, _ := s.View()
	wantState := s.ExternalView()
	rows, _, _, external, _, _ := s.EvaluationViewWithExternal(0, true)
	if len(rows) != 2 {
		t.Fatalf("expected latest and older tail observations, got %d", len(rows))
	}
	for i := range rows {
		e := rows[i].External
		if e == nil || e.Report == nil || e.Report.Sequence == nil {
			t.Fatalf("missing external report in row %d", i)
		}
		e.SourceGeneration = "caller-mutation"
		*e.Report.Sequence = 999
		e.Report.Fields["cpu.percent"] = model.FieldMissing
		*e.Report.Resource.CPU.Percent = 99
		e.Report.Resource.Disks[0].ID = "changed"
		*e.Report.Resource.Disks[0].Percent = 99
	}
	// The separately returned acceptance state must also remain detached.
	*external.Bindings["vm-resources"].Last.Resource.CPU.Percent = 88
	got, _ := s.View()
	if !reflect.DeepEqual(want, got) || !reflect.DeepEqual(wantState, s.ExternalView()) {
		t.Error("mutating the evaluation view changed persisted observations or reports")
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, dir, externalStoreOptions(c, &now))
	got, _ = reopened.View()
	if !reflect.DeepEqual(want, got) || !reflect.DeepEqual(wantState, reopened.ExternalView()) {
		t.Error("reader mutation survived checkpoint and restart")
	}
}

func TestEvaluationViewExternalMutationDoesNotRaceReaders(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := openTest(t, t.TempDir(), externalStoreOptions(c, &now))
	if err := s.Put(batch(c, externalStoreObservation(t, c, "sample-1", 20, 1, now, now))); err != nil {
		t.Fatal(err)
	}
	rows, _, _, _, _, _ := s.EvaluationViewWithExternal(0, true)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			*rows[0].External.Report.Resource.CPU.Percent = float64(i)
			rows[0].External.SourceGeneration = "caller-mutation"
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			s.EvaluationViewWithExternal(0, true)
		}
	}()
	close(start)
	wg.Wait()
	got, _ := s.View()
	if *got[0].External.Report.Resource.CPU.Percent != 20 || got[0].External.SourceGeneration != "gen-1" {
		t.Fatal("concurrent caller mutation changed stored evidence")
	}
}

func TestUnchangedExternalEvaluationAllocationBudget(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := &Store{beats: map[string]model.Heartbeat{}, evidenceVersion: 7, external: benchmarkExternalState(512, 64, now)}
	allocs := testing.AllocsPerRun(10, func() {
		rows, carries, _, external, _, version := s.EvaluationViewWithExternal(7, false)
		if rows != nil || carries != nil || external != nil || version != 7 {
			t.Fatal("unchanged evidence was copied or its version changed")
		}
	})
	if allocs > 16 {
		t.Fatalf("heartbeat-only evaluation allocated %.0f objects with unchanged external state", allocs)
	}
	_, _, _, external, _, _ := s.EvaluationViewWithExternal(7, true)
	if external == nil || len(external.Bindings) != 512 {
		t.Fatal("forced evaluation omitted external evidence")
	}
}

func BenchmarkExternalEvaluationHeartbeat(b *testing.B) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := &Store{beats: map[string]model.Heartbeat{}, evidenceVersion: 7, external: benchmarkExternalState(512, 64, now)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.EvaluationViewWithExternal(7, false)
	}
}
