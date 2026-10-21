package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func externalStoreConfig(t testing.TB) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Sources = []model.Source{{ID: "probe-api", Adapter: "synthetic-list", Observer: "home", Target: "https://probe.invalid/objects", CredentialEnv: "TOPH_PROBE_TOKEN", IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 100, MaxReportBytes: 16 << 10}}
	c.Checks = append(c.Checks, model.Check{ID: "vm-resources", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 60, PrimaryDisk: "root"}})
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func externalStoreOptions(c model.Config, now *time.Time) Options {
	bindings := map[string]model.ExternalBinding{}
	sources := map[string]model.Source{}
	for _, s := range c.Sources {
		sources[s.ID] = s
	}
	for _, x := range c.Checks {
		if x.Kind == "external" {
			bindings[x.ID] = *x.External
		}
	}
	return Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Bindings: bindings, Sources: sources, MaxHistory: 1, Now: func() time.Time { return *now }}
}

func externalStoreObservation(t testing.TB, c model.Config, sample string, value float64, seq uint64, attemptAt, sampledAt time.Time) model.Observation {
	t.Helper()
	x, _ := config.Check(c, "vm-resources")
	percent := value
	r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: x.External.Object, SampleID: sample, ObservedAt: sampledAt, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}, Disks: []model.DiskReport{{ID: "root", Percent: &percent}}}}
	return model.Observation{ID: "home.boot." + testkitSeq(seq), Project: c.Project, Contract: config.Contract(c, x), Check: x.ID, Observer: "home", Boot: "boot", Sequence: seq, SampledAt: attemptAt, Outcome: "ok", Evidence: "reported", Stage: "external_source", DurationMS: 4, PathEvidence: "external_source", External: &model.ExternalObservation{Source: "probe-api", SourceGeneration: "gen-1", AttemptedAt: attemptAt, Contact: true, Report: &r}}
}

func testkitSeq(n uint64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}

func TestExternalStatePersistsDeduplicatesAndDoesNotRefreshDeadline(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, externalStoreOptions(c, &now))
	first := externalStoreObservation(t, c, "sample-1", 20, 1, now.Add(-time.Minute), now.Add(-2*time.Minute))
	if err := s.Put(batch(c, first)); err != nil {
		t.Fatal(err)
	}
	state := s.ExternalView()
	if state == nil || state.Bindings["vm-resources"].ReportSamples != 1 || !state.Bindings["vm-resources"].DataDeadline.Equal(now.Add(-time.Minute)) {
		t.Fatalf("initial external state = %+v", state)
	}
	deadline := state.Bindings["vm-resources"].DataDeadline
	// A duplicate upstream sample arrives under a new observation identity
	// and a newer source read. Only contact moves.
	duplicate := externalStoreObservation(t, c, "sample-1", 20, 2, now, now.Add(-2*time.Minute))
	if err := s.Put(batch(c, duplicate)); err != nil {
		t.Fatal(err)
	}
	state = s.ExternalView()
	b := state.Bindings["vm-resources"]
	if b.ReportSamples != 1 || b.Duplicates != 1 || !b.DataDeadline.Equal(deadline) {
		t.Fatalf("duplicate advanced report: %+v", b)
	}
	if !state.Sources["probe-api"].LastContact.Equal(now) {
		t.Fatalf("duplicate did not advance contact: %+v", state.Sources)
	}
	// Same values under a new sample identity are a new sample.
	next := externalStoreObservation(t, c, "sample-2", 20, 3, now.Add(time.Second), now.Add(-time.Minute))
	if err := s.Put(batch(c, next)); err != nil {
		t.Fatal(err)
	}
	b = s.ExternalView().Bindings["vm-resources"]
	if b.ReportSamples != 2 || b.ResourceSamples != 2 || !b.DataDeadline.Equal(now) {
		t.Fatalf("same-value sample not accepted: %+v", b)
	}
	// Same sample identity with different content conflicts and does not
	// overwrite the last report.
	conflict := externalStoreObservation(t, c, "sample-2", 99, 4, now.Add(2*time.Second), now.Add(-time.Minute))
	if err := s.Put(batch(c, conflict)); err != nil {
		t.Fatal(err)
	}
	b = s.ExternalView().Bindings["vm-resources"]
	if b.Conflicts != 1 || b.ReportSamples != 2 || *b.Last.Resource.CPU.Percent != 20 {
		t.Fatalf("conflict overwrote state: %+v", b)
	}
	if len(s.rows) != 1 {
		t.Fatalf("raw history did not trim: %d", len(s.rows))
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, dir, externalStoreOptions(c, &now))
	state = reopened.ExternalView()
	if state == nil || state.Bindings["vm-resources"].ReportSamples != 2 || state.Bindings["vm-resources"].Duplicates != 1 || state.Bindings["vm-resources"].Conflicts != 1 {
		t.Fatalf("external state did not survive checkpoint: %+v", state)
	}
	// Raw history can be gone; the persisted cursor still names the replay.
	replay := externalStoreObservation(t, c, "sample-1", 20, 5, now.Add(3*time.Second), now.Add(-2*time.Minute))
	if err := reopened.Put(batch(c, replay)); err != nil {
		t.Fatal(err)
	}
	b = reopened.ExternalView().Bindings["vm-resources"]
	if b.Duplicates != 2 || b.ReportSamples != 2 || !b.DataDeadline.Equal(now) {
		t.Fatalf("trimmed replay not recognized: %+v", b)
	}
}

func TestExternalCheckpointUsesSchemaTwoAndStateBudget(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, externalStoreOptions(c, &now))
	if err := s.Put(batch(c, externalStoreObservation(t, c, "sample-1", 10, 1, now, now))); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := unwrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Schema   int                  `json:"schema"`
		External *model.ExternalState `json:"external"`
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatal(err)
	}
	if state.Schema != 2 || state.External == nil || state.External.Bindings["vm-resources"].ReportSamples != 1 {
		t.Fatalf("checkpoint schema/state = %d %+v", state.Schema, state.External)
	}
}

func TestExternalJournalReplayAndDuplicateReplay(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, externalStoreOptions(c, &now))
	obs := externalStoreObservation(t, c, "sample-1", 30, 1, now, now)
	if err := s.Put(batch(c, obs)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, dir, externalStoreOptions(c, &now))
	state := reopened.ExternalView()
	if state == nil || state.Bindings["vm-resources"].ReportSamples != 1 || !state.Bindings["vm-resources"].DataDeadline.Equal(now.Add(time.Minute)) {
		t.Fatalf("journal replay lost external state: %+v", state)
	}
	// Replaying the same observation identity is observation-id deduplication,
	// not a new report or a new source attempt.
	before := state.Sources["probe-api"]
	if err := reopened.Put(batch(c, obs)); err != nil {
		t.Fatal(err)
	}
	state = reopened.ExternalView()
	if state.Bindings["vm-resources"].ReportSamples != 1 || state.Bindings["vm-resources"].Duplicates != 0 || state.Sources["probe-api"].LastAttemptAt != before.LastAttemptAt {
		t.Fatalf("observation replay changed external state: %+v", state)
	}
}

func TestExternalSourceFailureDoesNotMoveContactOrReport(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, externalStoreOptions(c, &now))
	obs := externalStoreObservation(t, c, "sample-1", 30, 1, now, now)
	if err := s.Put(batch(c, obs)); err != nil {
		t.Fatal(err)
	}
	contact := s.ExternalView().Sources["probe-api"].LastContact
	failure := model.Observation{ID: "home.boot.2", Project: c.Project, Contract: config.Contract(c, c.Checks[len(c.Checks)-1]), Check: "vm-resources", Observer: "home", Boot: "boot", Sequence: 2, SampledAt: now.Add(time.Minute), Outcome: "error", Evidence: "reported", Stage: "external_source", Code: "source_timeout", Detail: "source read failed", DurationMS: 500, PathEvidence: "external_source", External: &model.ExternalObservation{Source: "probe-api", SourceGeneration: "gen-1", AttemptedAt: now.Add(time.Minute), Contact: false}}
	if err := s.Put(batch(c, failure)); err != nil {
		t.Fatal(err)
	}
	state := s.ExternalView()
	if !state.Sources["probe-api"].LastContact.Equal(contact) || state.Sources["probe-api"].LastResult != "error" || state.Sources["probe-api"].LastCode != "source_timeout" {
		t.Fatalf("source failure moved contact: %+v", state.Sources)
	}
	if state.Bindings["vm-resources"].ReportSamples != 1 || !state.Bindings["vm-resources"].DataDeadline.Equal(now.Add(time.Minute)) {
		t.Fatalf("source failure changed report: %+v", state.Bindings)
	}
}

func TestExternalJournalSyncFailureLeavesNoCursor(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	previous := syncFile
	defer func() { syncFile = previous }()
	s := openTest(t, t.TempDir(), externalStoreOptions(c, &now))
	syncFile = func(f *os.File) error { return os.ErrPermission }
	err := s.Put(batch(c, externalStoreObservation(t, c, "sample-1", 30, 1, now, now)))
	syncFile = previous
	if err == nil {
		t.Fatal("failed sync acknowledged")
	}
	if state := s.ExternalView(); state != nil && len(state.Bindings) > 0 {
		t.Fatalf("failed commit installed external state: %+v", state)
	}
	if rows, _ := s.View(); len(rows) != 0 {
		t.Fatal("failed commit stored rows")
	}
}

func TestNativeOnlyCheckpointRemainsSchemaOne(t *testing.T) {
	c := testkit.Config()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Now: func() time.Time { return now }})
	if err := s.Put(batch(c, testkit.Observation(c, "home-http", 1, now, "ok"))); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := unwrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Schema   int             `json:"schema"`
		External json.RawMessage `json:"external"`
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatal(err)
	}
	if state.Schema != 1 || len(state.External) != 0 {
		t.Fatalf("native checkpoint changed format: schema=%d external=%s", state.Schema, state.External)
	}
}

func TestExternalStateResetsOnContractChangeButNotNote(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := openTest(t, t.TempDir(), externalStoreOptions(c, &now))
	if err := s.Put(batch(c, externalStoreObservation(t, c, "sample-1", 10, 1, now, now))); err != nil {
		t.Fatal(err)
	}
	// A display-name change is outside the contract and keeps the report.
	renamed := config.Clone(c)
	renamed.Checks[len(renamed.Checks)-1].Name = "Renamed"
	renamed.Sources[0].Name = "Renamed source"
	config.Defaults(&renamed)
	s.SetExternalContext(renamed)
	s.SetCurrent(config.Revision(renamed), config.Contracts(renamed), config.Stale(renamed))
	if state := s.ExternalView(); state == nil || state.Bindings["vm-resources"].ReportSamples != 1 {
		t.Fatalf("name change discarded external state: %+v", state)
	}
	// A changed max age is a new binding contract: old report state cannot be
	// reused under it.
	changed := config.Clone(renamed)
	changed.Checks[len(changed.Checks)-1].External.MaxAgeSeconds = 120
	config.Defaults(&changed)
	if err := config.Validate(changed); err != nil {
		t.Fatal(err)
	}
	s.SetExternalContext(changed)
	s.SetCurrent(config.Revision(changed), config.Contracts(changed), config.Stale(changed))
	if state := s.ExternalView(); state != nil && len(state.Bindings) > 0 {
		t.Fatalf("changed contract reused external state: %+v", state)
	}
}
