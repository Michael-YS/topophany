package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func incrementalBinding(check, object, contract string, at time.Time, seen int) model.BindingState {
	percent := 25.0
	report := model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: object, SampleID: "sample-" + check, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample},
		Resource:   &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}},
	}
	state := model.BindingState{Check: check, Contract: contract, Source: "source", Object: object, ReportType: model.ReportTypeResource, MaxAgeSeconds: 60, Last: &report}
	for i := 0; i < seen; i++ {
		state.Seen = append(state.Seen, model.SeenSample{ID: fmt.Sprintf("%s-%03d", check, i), Hash: strings.Repeat("f", 64)})
	}
	return state
}

func TestExternalMergeClonesOnlyAffectedState(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	bindingA := model.ExternalBinding{Source: "source", Object: "obj-a", Report: model.ReportTypeResource, MaxAgeSeconds: 60}
	bindingB := model.ExternalBinding{Source: "source", Object: "obj-b", Report: model.ReportTypeResource, MaxAgeSeconds: 60}
	s := &Store{
		options: Options{
			Bindings: map[string]model.ExternalBinding{"a": bindingA, "b": bindingB},
			Sources:  map[string]model.Source{"source": {ID: "source", Observer: "home"}},
		},
		externalSizes:       map[string]int64{},
		externalSourceBytes: map[string]int64{},
	}
	s.external = model.NewExternalState()
	s.external.Sources["source"] = model.SourceState{ID: "source", LastContact: now.Add(-time.Minute), LastAttemptAt: now.Add(-time.Minute), LastResult: "ok"}
	s.external.Bindings["a"] = incrementalBinding("a", "obj-a", "contract-a", now.Add(-time.Minute), 64)
	s.external.Bindings["b"] = incrementalBinding("b", "obj-b", "contract-b", now.Add(-time.Minute), 64)
	s.rebuildExternalAccountingLocked()
	beforeB := s.external.Bindings["b"].Last
	beforeABytes, beforeBSize := s.externalSizes["b:a"], s.externalSizes["b:b"]
	beforeExternalBytes := s.externalBytes

	percent := 30.0
	report := model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "obj-a", SampleID: "new-a", ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample},
		Resource:   &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}},
	}
	obs := model.Observation{ID: "home.boot.2", Contract: "contract-a", Check: "a", ReceivedAt: now, External: &model.ExternalObservation{Source: "source", AttemptedAt: now, Contact: true, Report: &report}}
	commit, err := s.mergeExternalLocked([]model.Observation{obs})
	if err != nil {
		t.Fatal(err)
	}
	if commit == nil {
		t.Fatal("merge returned no commit")
	}
	if commit.state.Bindings["b"].Last != beforeB {
		t.Fatal("unaffected binding was cloned")
	}
	if commit.state.Bindings["a"].Last == s.external.Bindings["a"].Last {
		t.Fatal("affected binding was not detached")
	}
	if len(commit.sizes) != 2 || commit.sizes["b:a"] == 0 || commit.sizes["s:source"] == 0 {
		t.Fatalf("incremental sizes = %+v", commit.sizes)
	}
	if _, ok := commit.sizes["b:b"]; ok {
		t.Fatalf("unaffected binding was re-serialized: %+v", commit.sizes)
	}
	if s.external.Bindings["a"].ReportSamples != 0 || s.externalBytes != beforeExternalBytes || s.externalSizes["b:a"] != beforeABytes || s.externalSizes["b:b"] != beforeBSize {
		t.Fatal("merge mutated live state or accounting")
	}
	s.installExternalLocked(commit)
	if s.external.Bindings["a"].ReportSamples != 1 || s.external.Bindings["b"].Last != beforeB {
		t.Fatalf("installed state = %+v", s.external.Bindings)
	}
	total, _, _, err := s.externalAccountingLocked()
	if err != nil {
		t.Fatal(err)
	}
	if total != s.externalBytes || total != commit.size {
		t.Fatalf("incremental accounting = %d, rebuilt = %d, commit = %d", s.externalBytes, total, commit.size)
	}
}

func TestExternalSourceShareCannotConsumeTheWholeBudget(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	binding := model.ExternalBinding{Source: "source", Object: "obj-a", Report: model.ReportTypeResource, MaxAgeSeconds: 60}
	s := &Store{
		options: Options{
			Bindings: map[string]model.ExternalBinding{"a": binding},
			Sources: map[string]model.Source{
				"source": {ID: "source", Observer: "home"},
				"other":  {ID: "other", Observer: "home"},
			},
		},
		externalSizes:       map[string]int64{},
		externalSourceBytes: map[string]int64{},
	}
	s.external = model.NewExternalState()
	s.external.Sources["source"] = model.SourceState{ID: "source"}
	s.external.Bindings["a"] = incrementalBinding("a", "obj-a", "contract-a", now.Add(-time.Minute), 1)
	s.rebuildExternalAccountingLocked()
	// Model a source that has already reached its fair share. The next report
	// must be refused before journal acknowledgement, without evicting other
	// sources' accepted state.
	s.externalSourceBytes["source"] = maxExternalSourceBytes
	percent := 30.0
	report := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "obj-a", SampleID: "new-a", ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}}}
	obs := model.Observation{ID: "home.boot.2", Contract: "contract-a", Check: "a", External: &model.ExternalObservation{Source: "source", AttemptedAt: now, Contact: true, Report: &report}}
	if _, err := s.mergeExternalLocked([]model.Observation{obs}); err == nil || !strings.Contains(err.Error(), "source state budget") {
		t.Fatalf("oversized source write = %v", err)
	}
}

func benchmarkExternalState(bindings, seen int, at time.Time) *model.ExternalState {
	state := model.NewExternalState()
	state.Sources["source"] = model.SourceState{ID: "source", LastContact: at, LastAttemptAt: at, LastResult: "ok"}
	for i := 0; i < bindings; i++ {
		check := fmt.Sprintf("check-%03d", i)
		state.Bindings[check] = incrementalBinding(check, fmt.Sprintf("obj-%03d", i), strings.Repeat("c", 64), at, seen)
	}
	return state
}

func BenchmarkNativeStorePutWithExternalState(b *testing.B) {
	for _, bindings := range []int{0, 128, 512} {
		b.Run(fmt.Sprint(bindings), func(b *testing.B) {
			now := time.Now().UTC()
			s, err := Open(b.TempDir(), Options{MaxHistory: 100, MaxReceipts: 100000})
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			s.external = benchmarkExternalState(bindings, 64, now)
			s.rebuildExternalAccountingLocked()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				obs := model.Observation{ID: fmt.Sprintf("home.boot.%d", i+1), Project: "bench", Check: "native", Observer: "home", Boot: "boot", Sequence: uint64(i + 1), SampledAt: now, Outcome: "ok", Evidence: "measured", DurationMS: 1}
				if err := s.Put(model.Batch{Schema: model.BatchSchema, Project: "bench", Observations: []model.Observation{obs}}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkExternalMergeAffectedBinding(b *testing.B) {
	for _, bindings := range []int{1, 128, 512} {
		b.Run(fmt.Sprint(bindings), func(b *testing.B) {
			now := time.Now().UTC()
			binding := model.ExternalBinding{Source: "source", Object: "obj-000", Report: model.ReportTypeResource, MaxAgeSeconds: 60}
			s := &Store{
				options: Options{
					Bindings: map[string]model.ExternalBinding{"check-000": binding},
					Sources:  map[string]model.Source{"source": {ID: "source", Observer: "home"}},
				},
				externalSizes:       map[string]int64{},
				externalSourceBytes: map[string]int64{},
			}
			s.external = benchmarkExternalState(bindings, 64, now)
			s.rebuildExternalAccountingLocked()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				percent := 20.0 + float64(i%10)
				report := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "obj-000", SampleID: fmt.Sprintf("sample-%d", i+1), ObservedAt: now.Add(time.Duration(i+1) * time.Millisecond), TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}}}
				obs := model.Observation{ID: fmt.Sprintf("home.boot.%d", i+1), Contract: strings.Repeat("c", 64), Check: "check-000", ReceivedAt: now, External: &model.ExternalObservation{Source: "source", AttemptedAt: now, Contact: true, Report: &report}}
				if _, err := s.mergeExternalLocked([]model.Observation{obs}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestNativePutDoesNotCloneExternalState(t *testing.T) {
	now := time.Now().UTC()
	s, err := Open(t.TempDir(), Options{MaxHistory: 100, MaxReceipts: 100000})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.external = benchmarkExternalState(512, 64, now)
	s.rebuildExternalAccountingLocked()
	before := s.external
	sequence := 0
	allocs := testing.AllocsPerRun(5, func() {
		sequence++
		obs := model.Observation{ID: fmt.Sprintf("home.boot.%d", sequence), Project: "bench", Check: "native", Observer: "home", Boot: "boot", Sequence: uint64(sequence), SampledAt: now, Outcome: "ok", Evidence: "measured", DurationMS: 1}
		if err := s.Put(model.Batch{Schema: model.BatchSchema, Project: "bench", Observations: []model.Observation{obs}}); err != nil {
			t.Fatal(err)
		}
	})
	if s.external != before {
		t.Fatal("native Put replaced or cloned external state")
	}
	// A full external clone for this fixture adds tens of thousands of Seen
	// allocations. The native transaction must stay near its own journal and
	// receipt cost regardless of the external Seen total.
	if allocs > 300 {
		t.Fatalf("native Put allocated %.0f objects with external state present", allocs)
	}
}
