package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestExternalReloadPrunesCheckpointBeforeJournalReplay(t *testing.T) {
	c := externalStoreConfig(t)
	c.Checks = c.Checks[:len(c.Checks)-1]
	c.Sources[0].MaxObjects = model.MaxExternalChecks
	for i := 0; i < model.MaxExternalChecks; i++ {
		c.Checks = append(c.Checks, model.Check{
			ID: fmt.Sprintf("status-%03d", i), Observer: "home", Kind: "external",
			External: &model.ExternalBinding{Source: "probe-api", Object: fmt.Sprintf("object-%03d", i), Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 60},
		})
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, externalStoreOptions(c, &now))
	observation := func(c model.Config, x model.Check, sequence uint64) model.Observation {
		report := model.ExternalReport{
			ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: x.External.Object, SampleID: "sample-" + x.ID, ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample},
			Status:     &model.StatusReport{Status: "up"},
		}
		return model.Observation{ID: "home.boot." + testkitSeq(sequence), Project: c.Project, Contract: config.Contract(c, x), Check: x.ID, Observer: "home", Boot: "boot", Sequence: sequence, SampledAt: now, Outcome: "ok", Evidence: "reported", Stage: "external_source", PathEvidence: "external_source", External: &model.ExternalObservation{Source: "probe-api", SourceGeneration: "gen-1", AttemptedAt: now, Contact: true, Report: &report}}
	}
	var initial []model.Observation
	for _, x := range c.Checks {
		if x.Kind == "external" {
			initial = append(initial, observation(c, x, uint64(len(initial)+1)))
		}
	}
	if err := s.Put(batch(c, initial...)); err != nil {
		t.Fatal(err)
	}
	if got := len(s.ExternalView().Bindings); got != model.MaxExternalChecks {
		t.Fatalf("initial binding count = %d", got)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Reload frees one slot, then accepts a replacement into the WAL while
	// the durable checkpoint still contains the removed binding.
	next := config.Clone(c)
	x := &next.Checks[len(next.Checks)-1]
	removed := x.ID
	x.ID, x.External.Object = "replacement", "replacement-object"
	config.Defaults(&next)
	if err := config.Validate(next); err != nil {
		t.Fatal(err)
	}
	s.SetExternalContext(next)
	s.SetCurrent(config.Revision(next), config.Contracts(next), config.Stale(next))
	if err := s.Put(batch(next, observation(next, *x, model.MaxExternalChecks+1))); err != nil {
		t.Fatalf("replacement was not acknowledged: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil || !bytes.Equal(checkpoint, unchanged) {
		t.Fatalf("fixture must retain the pre-reload checkpoint: %v", err)
	}
	reopened := openTest(t, dir, externalStoreOptions(next, &now))
	state := reopened.ExternalView()
	if len(state.Bindings) != model.MaxExternalChecks {
		t.Fatalf("replayed binding count = %d", len(state.Bindings))
	}
	if _, present := state.Bindings[removed]; present {
		t.Fatal("removed binding survived recovery")
	}
	if got := state.Bindings["replacement"]; got.Last == nil || got.ReportSamples != 1 || got.Last.Status.Status != "up" {
		t.Fatalf("acknowledged replacement was not recovered: %+v", got)
	}
}
