package store

import (
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutomaticCheckpointPreservesExternalStateAndFormat(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Now().UTC()
	opts := externalStoreOptions(c, &now)
	opts.CheckpointBytes, opts.MaxJournalBytes = 1024, 64<<10
	dir := t.TempDir()
	s := openTest(t, dir, opts)
	v := externalStoreObservation(t, c, "sample-1", 90, 1, now, now)
	v.Detail = strings.Repeat("x", 800) // exceed the automatic checkpoint floor
	if err := s.Put(batch(c, v)); err != nil {
		t.Fatal(err)
	}
	want := s.ExternalView()
	// Close waits for automatic checkpoint installation; do not mask this
	// path by invoking the synchronous Checkpoint method.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	schema, state := checkpointSchema(t, dir)
	if schema != 2 || !reflect.DeepEqual(state, want) {
		t.Fatalf("automatic checkpoint lost format/state: schema=%d state=%+v", schema, state)
	}
	reopened := openTest(t, dir, opts)
	if got := reopened.ExternalView(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restart changed external state: got=%+v want=%+v", got, want)
	}
	duplicate := externalStoreObservation(t, c, "sample-1", 90, 2, now, now)
	if err := reopened.Put(batch(c, duplicate)); err != nil {
		t.Fatal(err)
	}
	got := reopened.ExternalView().Bindings["vm-resources"]
	if got.ReportSamples != 1 || got.Duplicates != 1 {
		t.Fatalf("automatic checkpoint lost deduplication: %+v", got)
	}
}

func TestExternalBackgroundCheckpointPreservesConcurrentTail(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Now().UTC()
	opts := externalStoreOptions(c, &now)
	opts.CheckpointBytes, opts.MaxJournalBytes = 1024, 64<<10
	s := openTest(t, t.TempDir(), opts)
	original := installCheckpoint
	paused, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	installCheckpoint = func(path string, write func(io.WriteSeeker) error) error {
		// The first call is the synchronous format barrier. Pause only the
		// first automatic snapshot, after it has captured its owned state.
		if calls.Add(1) == 2 {
			close(paused)
			<-release
		}
		return original(path, write)
	}
	released := false
	defer func() {
		if !released {
			close(release)
		}
		_ = s.Close()
		installCheckpoint = original
	}()
	first := externalStoreObservation(t, c, "sample-1", 20, 1, now, now)
	first.Detail = strings.Repeat("x", 800)
	if err := s.Put(batch(c, first)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic checkpoint did not start")
	}
	second := externalStoreObservation(t, c, "sample-2", 30, 2, now.Add(time.Second), now.Add(time.Second))
	if err := s.Put(batch(c, second)); err != nil {
		t.Fatal(err)
	}
	want := s.ExternalView()
	close(release)
	released = true
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, captured := checkpointSchema(t, s.dir)
	if captured == nil || captured.Bindings["vm-resources"].ReportSamples != 1 {
		t.Fatalf("checkpoint aliased the later write: %+v", captured)
	}
	reopened := openTest(t, s.dir, opts)
	if got := reopened.ExternalView(); !reflect.DeepEqual(got, want) {
		t.Fatalf("checkpoint plus journal tail lost reports: got=%+v want=%+v", got, want)
	}
}
