package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func externalEngineConfig(t testing.TB, reportType string, assertions *model.ResourceAssertions) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Sources = []model.Source{{ID: "probe-api", Adapter: "synthetic-list", Observer: "home", Target: "https://probe.invalid/objects", IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 100, MaxReportBytes: 16 << 10}}
	binding := &model.ExternalBinding{Source: "probe-api", Object: "vm/dmit", Report: reportType, Node: "dmit", MaxAgeSeconds: 300, Assertions: assertions}
	if reportType == model.ReportTypeResource {
		binding.PrimaryDisk = "root"
	}
	x := model.Check{ID: "external-check", Observer: "home", Kind: "external", Service: "web", FailureThreshold: 1, RecoveryThreshold: 1, External: binding}
	c.Checks = append(c.Checks, x)
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func externalState(t testing.TB, c model.Config, at time.Time, rep model.ExternalReport, contact time.Time) *model.ExternalState {
	t.Helper()
	x, _ := config.Check(c, "external-check")
	b, _ := config.Source(c, x.External.Source)
	normalized, err := report.Normalize(rep, b.MaxReportBytes)
	if err != nil {
		t.Fatal(err)
	}
	ext := model.NewExternalState()
	ext.Sources["probe-api"] = report.MergeSource(model.SourceState{}, "probe-api", contact, true, "", 5)
	state := model.BindingState{FailureThreshold: x.FailureThreshold, RecoveryThreshold: x.RecoveryThreshold, PolicyThreshold: x.PolicyThreshold}
	state, _ = report.MergeBinding(x.ID, config.Contract(c, x), *x.External, state, normalized, at)
	ext.Bindings[x.ID] = state
	return ext
}

func statusReport(at time.Time, status string) model.ExternalReport {
	return model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", SampleID: "sample-" + status, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: status}}
}

func resourceReport(at time.Time, cpu float64) model.ExternalReport {
	return model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", SampleID: "resource-1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu}, Disks: []model.DiskReport{{ID: "root", Percent: &cpu}}}}
}

func TestExternalInactiveChecksBeforeFirstReport(t *testing.T) {
	for _, mode := range []struct {
		name                  string
		disabled, maintenance bool
		freshness             string
	}{
		{"paused", true, false, "paused"},
		{"maintenance", false, true, "maintenance"},
		{"paused_takes_precedence", true, true, "paused"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			c := externalEngineConfig(t, model.ReportTypeStatus, nil)
			x := &c.Checks[len(c.Checks)-1]
			x.Disabled, x.Maintenance = mode.disabled, mode.maintenance
			now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
			plan := NewPlan(c)
			assertInactiveCheck := func(t *testing.T, snap model.Snapshot) model.CheckState {
				t.Helper()
				var checked model.CheckState
				for _, state := range snap.Checks {
					if state.ID == "external-check" {
						checked = state
					}
				}
				if checked.ID == "" || checked.Freshness != mode.freshness || checked.Health != "unknown" || checked.PolicyConfirmed {
					t.Fatalf("inactive external check = %+v", checked)
				}
				return checked
			}
			assertInactive := func(t *testing.T, snap model.Snapshot) model.ExternalBindingView {
				t.Helper()
				checked := assertInactiveCheck(t, snap)
				if len(snap.External) != 1 || snap.External[0].Freshness != mode.freshness || snap.External[0].Health != "unknown" || snap.External[0].Reason != checked.Reason {
					t.Fatalf("inactive external view = %+v", snap.External)
				}
				return snap.External[0]
			}
			t.Run("one-shot_builder", func(t *testing.T) {
				// Plan.Build publishes check states; only Prepared.Build adds
				// the separate External projection to the snapshot.
				assertInactiveCheck(t, plan.Build("test", "", nil, nil, nil, now))
			})
			sourceFailure := model.NewExternalState()
			sourceFailure.Sources["probe-api"] = report.MergeSource(model.SourceState{}, "probe-api", now, false, "auth_failed", 5)
			emptyBinding := model.NewExternalState()
			emptyBinding.Bindings["external-check"] = model.BindingState{Source: "probe-api"}
			accepted := externalState(t, c, now, statusReport(now, "down"), now)
			for name, ext := range map[string]*model.ExternalState{
				"no_state":        nil,
				"no_binding":      model.NewExternalState(),
				"source_failure":  sourceFailure,
				"empty_binding":   emptyBinding,
				"accepted_report": accepted,
			} {
				t.Run(name, func(t *testing.T) {
					e := assertInactive(t, plan.PrepareExternal(nil, nil, nil, ext).Build("test", "", now))
					if name == "source_failure" && (e.SourceState.LastResult != "error" || e.SourceState.LastCode != "auth_failed") {
						t.Fatalf("inactive binding lost source diagnostics: %+v", e.SourceState)
					}
					if name == "accepted_report" && (e.Report == nil || e.Report.Status == nil || e.Report.Status.Status != "down" || e.Stats.Samples != 1) {
						t.Fatalf("inactive binding lost historical report: %+v", e)
					}
				})
			}
		})
	}
}

func TestExternalStatusIsReportedAndDoesNotVoteInMeasuredService(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeStatus, nil)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	ext := externalState(t, c, now.Add(-10*time.Second), statusReport(now.Add(-10*time.Second), "down"), now.Add(-5*time.Second))
	rows := []model.Observation{testkit.Observation(c, "home-http", 1, now.Add(-time.Second), "ok")}
	prepared := NewPlan(c).PrepareExternal(rows, nil, map[string]model.Heartbeat{}, ext)
	snap := prepared.Build("test", "", now)
	var checked model.CheckState
	for _, cs := range snap.Checks {
		if cs.ID == "external-check" {
			checked = cs
		}
	}
	if checked.Health != "down" || checked.Evidence != "reported" || checked.Last != nil {
		t.Fatalf("external status = %+v", checked)
	}
	if len(snap.External) != 1 {
		t.Fatalf("external projection = %+v", snap.External)
	}
	e := snap.External[0]
	if e.Health != "down" || e.Report == nil || e.Stats.Samples != 1 || e.SampledAt != now.Add(-10*time.Second) {
		t.Fatalf("external view = %+v", e)
	}
	if !e.DataDeadline.Equal(now.Add(290*time.Second)) || e.ValidUntil.IsZero() {
		t.Fatalf("external deadlines = %+v", e)
	}
	without := NewPlan(c).PrepareExternal(rows, nil, map[string]model.Heartbeat{}, nil).Build("test", "", now)
	for i := range snap.Services {
		if snap.Services[i].ID != without.Services[i].ID || snap.Services[i].Health != without.Services[i].Health || len(snap.Services[i].Checks) != len(without.Services[i].Checks) {
			t.Fatalf("external status voted in measured service health: %+v vs %+v", snap.Services[i], without.Services[i])
		}
	}
	if len(snap.Incidents) != 0 {
		t.Fatalf("external status fabricated a measured incident: %+v", snap.Incidents)
	}
}

func TestExternalReportDeadlinesAndSourceFailure(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeStatus, nil)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := now.Add(-10 * time.Second)
	ext := externalState(t, c, at, statusReport(at, "up"), at)
	prepared := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext)
	fresh := prepared.Build("test", "", now)
	if len(fresh.External) != 1 || fresh.External[0].Health != "up" {
		t.Fatalf("fresh external = %+v", fresh.External)
	}
	// Source failure does not delete the last accepted report, but its contact
	// deadline still expires from the last successful read.
	stale := prepared.Build("test", "", now.Add(2*time.Minute))
	if stale.External[0].Health != "unknown" || stale.External[0].Freshness != "stale" {
		t.Fatalf("expired contact = %+v", stale.External[0])
	}
	if stale.External[0].Stats.Samples != 1 {
		t.Fatal("expiry changed the report sample count")
	}
}

func TestExternalResourcePolicyUsesIndependentStats(t *testing.T) {
	limit := 80.0
	c := externalEngineConfig(t, model.ReportTypeResource, &model.ResourceAssertions{CPUPercentMax: &limit})
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := now.Add(-10 * time.Second)
	ext := externalState(t, c, at, resourceReport(at, 91), at)
	snap := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext).Build("test", "", now)
	if len(snap.External) != 1 {
		t.Fatal("missing resource view")
	}
	e := snap.External[0]
	if e.Health != "degraded" || e.Policy == nil || !e.Policy.Confirmed || e.Stats.ResourceSamples != 1 || e.Stats.Samples != 1 || e.Stats.UpstreamLatencySamples != 0 {
		t.Fatalf("resource policy = %+v", e)
	}
	for _, cs := range snap.Checks {
		if cs.ID == "external-check" && (cs.Health != "degraded" || len(cs.Availability) != 0) {
			t.Fatalf("external check state = %+v", cs)
		}
	}
}

func TestExternalDataAndContactDeadlinesAreIndependent(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeStatus, nil)
	// Make data age small and contact stale large.
	c.Checks[len(c.Checks)-1].External.MaxAgeSeconds = 30
	c.Sources[0].StaleSeconds = 600
	c.Checks[len(c.Checks)-1].StaleSeconds = 600
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute)
	ext := externalState(t, c, at, statusReport(at, "up"), now.Add(-time.Second))
	snap := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext).Build("test", "", now)
	e := snap.External[0]
	if e.Health != "unknown" || e.Freshness != "stale" || !e.DataDeadline.Equal(at.Add(30*time.Second)) {
		t.Fatalf("data deadline = %+v", e)
	}
	if e.ContactDeadline.IsZero() || e.ValidUntil.After(e.DataDeadline) {
		t.Fatalf("contact/view deadline = %+v", e)
	}
}

func TestExternalSourceErrorDoesNotRecoverOrFailTarget(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeStatus, nil)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := now.Add(-30 * time.Second)
	ext := externalState(t, c, at, statusReport(at, "down"), now.Add(-time.Second))
	// A newer failed source read updates diagnostics but not contact.
	ext.Sources["probe-api"] = report.MergeSource(ext.Sources["probe-api"], "probe-api", now, false, "auth_failed", 500)
	snap := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext).Build("test", "", now)
	e := snap.External[0]
	if e.Health != "down" || e.SourceState.LastResult != "error" || e.SourceState.LastCode != "auth_failed" {
		t.Fatalf("source failure changed target: %+v", e)
	}
	// Once contact expires, the target becomes unknown, not recovered.
	later := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext).Build("test", "", now.Add(2*time.Minute))
	if later.External[0].Health != "unknown" || later.External[0].Freshness != "stale" {
		t.Fatalf("expiry fabricated a state: %+v", later.External[0])
	}
}

func TestUpstreamLatencyIsNotCollectionDuration(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeStatus, nil)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := now.Add(-10 * time.Second)
	latency := int64(20)
	r := statusReport(at, "up")
	r.SampleID = "latency-1"
	r.Status.UpstreamLatencyMS = &latency
	ext := externalState(t, c, at, r, at)
	snap := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext).Build("test", "", now)
	e := snap.External[0]
	if e.Stats.UpstreamLatencySamples != 1 || e.Stats.UpstreamLatencyAvgMS == nil || *e.Stats.UpstreamLatencyAvgMS != 20 || e.Stats.UpstreamLatencyMaxMS == nil || *e.Stats.UpstreamLatencyMaxMS != 20 {
		t.Fatalf("upstream latency = %+v", e.Stats)
	}
	if e.Stats.Samples != 1 || e.Stats.StatusSamples != 1 {
		t.Fatalf("status sample stats = %+v", e.Stats)
	}
}

func richResourceReport(at time.Time) model.ExternalReport {
	cpu, memory, disk := 25.0, 50.0, 20.0
	total, used := uint64(1024), uint64(512)
	diskTotal, diskUsed := uint64(4096), uint64(1024)
	sequence := uint64(7)
	return model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", SampleID: "rich-1", Epoch: "boot-a", Sequence: &sequence, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample, Fields: map[string]string{"cpu.percent": model.FieldKnown, "disk.root.percent": model.FieldKnown}},
		Resource: &model.ResourceReport{
			CPU:    &model.CPUReport{Percent: &cpu, Basis: "guest_cpu"},
			Memory: &model.MemoryReport{TotalBytes: &total, UsedBytes: &used, Percent: &memory, Basis: "available"},
			Disks:  []model.DiskReport{{ID: "root", Path: "/", TotalBytes: &diskTotal, UsedBytes: &diskUsed, Percent: &disk, Basis: "free"}},
		},
	}
}

func TestExternalSnapshotOwnsEveryMutableReportMember(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeResource, nil)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := now.Add(-10 * time.Second)
	ext := externalState(t, c, at, richResourceReport(at), at)
	prepared := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext)

	first := prepared.Build("test", "", now)
	if len(first.External) != 1 || first.External[0].Report == nil {
		t.Fatalf("first snapshot = %+v", first.External)
	}
	report := first.External[0].Report
	*report.Resource.CPU.Percent = 99
	report.Resource.CPU.Basis = "mutated"
	*report.Resource.Memory.UsedBytes = 1
	*report.Resource.Memory.Percent = 99
	*report.Resource.Disks[0].Percent = 99
	report.Resource.Disks[0].Path = "/mutated"
	report.Fields["cpu.percent"] = model.FieldMissing
	*report.Sequence = 42
	*report.Resource.Memory.TotalBytes = 1
	*report.Resource.Disks[0].TotalBytes = 1

	second := prepared.Build("test", "", now)
	secondReport := second.External[0].Report
	if *secondReport.Resource.CPU.Percent != 25 || secondReport.Resource.CPU.Basis != "guest_cpu" {
		t.Fatalf("CPU alias = %+v", secondReport.Resource.CPU)
	}
	if *secondReport.Resource.Memory.UsedBytes != 512 || *secondReport.Resource.Memory.Percent != 50 || secondReport.Fields["cpu.percent"] != model.FieldKnown {
		t.Fatalf("memory/fields alias = %+v %+v", secondReport.Resource.Memory, secondReport.Fields)
	}
	if secondReport.Resource.Disks[0].Path != "/" || *secondReport.Resource.Disks[0].Percent != 20 {
		t.Fatalf("disk alias = %+v", secondReport.Resource.Disks[0])
	}
	if secondReport.Sequence == nil || *secondReport.Sequence != 7 {
		t.Fatalf("sequence alias = %+v", secondReport.Sequence)
	}
	if persisted := ext.Bindings["external-check"].Last; persisted == nil || *persisted.Resource.CPU.Percent != 25 || persisted.Resource.Disks[0].Path != "/" {
		t.Fatalf("persisted state was mutated through a snapshot: %+v", persisted)
	}

	// Heartbeat refreshes share immutable prepared state; each published
	// snapshot still owns its reports independently.
	refreshed := prepared.WithHeartbeats(nil)
	third := refreshed.Build("test", "", now)
	third.External[0].Report.Resource.Disks[0].ID = "changed"
	if got := refreshed.Build("test", "", now).External[0].Report.Resource.Disks[0].ID; got != "root" {
		t.Fatalf("heartbeat snapshot aliased its cache: %q", got)
	}
}

func TestExternalSnapshotBuildConcurrentMutation(t *testing.T) {
	c := externalEngineConfig(t, model.ReportTypeResource, nil)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := now.Add(-10 * time.Second)
	ext := externalState(t, c, at, richResourceReport(at), at)
	prepared := NewPlan(c).PrepareExternal(nil, nil, map[string]model.Heartbeat{}, ext)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				snap := prepared.Build("test", "", now)
				*snap.External[0].Report.Resource.CPU.Percent = 99
				snap.External[0].Report.Fields["cpu.percent"] = model.FieldMissing
			}
		}()
	}
	wg.Wait()
	final := prepared.Build("test", "", now)
	if *final.External[0].Report.Resource.CPU.Percent != 25 || final.External[0].Report.Fields["cpu.percent"] != model.FieldKnown {
		t.Fatalf("concurrent snapshot mutation corrupted the cache: %+v", final.External[0].Report)
	}
}
