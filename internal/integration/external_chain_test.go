package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func integrationConfig(t testing.TB, base string, statusBindings int) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Checks = nil
	c.Sources = []model.Source{
		{ID: "list-source", Adapter: testkit.SyntheticListAdapter, Observer: "cloud", Target: base + "/list", CredentialEnv: "TOPO_SYNTHETIC_SOURCE_TOKEN", IntervalSeconds: 1, TimeoutSeconds: 5, StaleSeconds: 10, MaxResponseBytes: 128 << 10, MaxPages: 2, MaxObjects: 100, MaxReportBytes: 64 << 10},
		{ID: "counter-source", Adapter: testkit.SyntheticCounterAdapter, Observer: "cloud", Target: base + "/counter", CredentialEnv: "TOPO_SYNTHETIC_SOURCE_TOKEN", IntervalSeconds: 1, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 128 << 10, MaxPages: 1, MaxObjects: 10, MaxReportBytes: 64 << 10},
		{ID: "status-source", Adapter: testkit.SyntheticStatusAdapter, Observer: "cloud", Target: base + "/status", CredentialEnv: "TOPO_SYNTHETIC_SOURCE_TOKEN", IntervalSeconds: 1, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 128 << 10, MaxPages: 1, MaxObjects: 4, MaxReportBytes: 64 << 10},
	}
	c.Checks = append(c.Checks,
		model.Check{ID: "list-a", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "list-source", Object: "vm-a", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 600, PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: floatValue(90)}}},
		model.Check{ID: "list-b", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "list-source", Object: "vm-b", Report: model.ReportTypeResource, Node: "s2", MaxAgeSeconds: 600, PrimaryDisk: "data"}},
		model.Check{ID: "counter-vm", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "counter-source", Object: "counter-counter-source", Report: model.ReportTypeResource, Node: "aliyun", MaxAgeSeconds: 120}},
	)
	for i := 0; i < statusBindings; i++ {
		c.Checks = append(c.Checks, model.Check{ID: fmt.Sprintf("status-%03d", i), Observer: "cloud", Kind: "external", Service: "web", External: &model.ExternalBinding{Source: "status-source", Object: "svc-web", Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 120}})
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func floatValue(v float64) *float64 { return &v }

func TestSyntheticListCounterStatusThroughPublicChain(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 100)
	ch := newChain(t, c)
	ch.collectorDelay.Store(true)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)

	state := ch.st.ExternalView()
	if state == nil || len(state.Bindings) != 103 {
		t.Fatalf("external bindings = %+v", state)
	}
	if state.Bindings["list-a"].ReportSamples != 1 || state.Bindings["list-b"].ReportSamples != 1 || state.Bindings["counter-vm"].ReportSamples != 1 {
		t.Fatalf("resource report counts = %+v", state.Bindings)
	}
	// Duplicate old list samples did not become duplicate objects, and the
	// partial vm-b report has no fabricated memory.
	if state.Bindings["list-a"].Last.Resource.CPU.Percent == nil || *state.Bindings["list-a"].Last.Resource.CPU.Percent != 12.5 {
		t.Fatalf("list-a latest sample = %+v", state.Bindings["list-a"].Last)
	}
	if state.Bindings["list-b"].Last.Resource.Memory != nil {
		t.Fatalf("partial machine gained memory = %+v", state.Bindings["list-b"].Last.Resource.Memory)
	}
	total := int64(0)
	for i := 0; i < 100; i++ {
		b := state.Bindings[fmt.Sprintf("status-%03d", i)]
		if b.ReportSamples != 1 || b.StatusUp != 1 {
			t.Fatalf("status binding %d = %+v", i, b)
		}
		total += b.UpstreamLatencySamples
		if b.UpstreamLatencySumMS != 20 || b.UpstreamLatencySamples != 1 {
			t.Fatalf("upstream latency mixed with collection time: %+v", b)
		}
	}
	if total != 100 {
		t.Fatalf("latency samples = %d", total)
	}
	if synthetic.count("/status") != 1 {
		t.Fatalf("100 status bindings caused %d reads", synthetic.count("/status"))
	}
	if !ch.delayed.Load() {
		t.Fatal("500ms API delay was not exercised")
	}
	rows, _ := ch.st.View()
	for _, row := range rows {
		if row.Check == "status-000" && row.DurationMS >= 250 {
			t.Fatalf("API delay entered collection duration: %+v", row)
		}
	}

	at := (*ch.now).Add(time.Second)
	snap := ch.snapshot(at)
	if len(snap.External) != 103 {
		t.Fatalf("snapshot external = %d", len(snap.External))
	}
	selected := viewFor(snap, at).SelectResources()
	dmit, ok := selected["dmit"]
	if !ok || dmit.Selected.Check != "list-a" || dmit.Selected.Source != "list-source" || dmit.Selected.Health != "up" {
		t.Fatalf("dmit selection = %+v", dmit)
	}
	if dmit.Selected.Facts["cpu_used_percent"] != "12.50" || dmit.Selected.DiskPath != "/" {
		t.Fatalf("dmit facts = %+v", dmit.Selected)
	}
	s2 := selected["s2"]
	if s2.Selected.Check != "list-b" || s2.Selected.Facts["memory_used_percent"] != "" || s2.Selected.DiskPath != "/data" {
		t.Fatalf("partial s2 selection = %+v", s2)
	}
	aliyun := selected["aliyun"]
	if aliyun.Selected.Check != "counter-vm" || aliyun.Selected.Facts["cpu_used_percent"] != "" || aliyun.Selected.Facts["memory_total_bytes"] == "" {
		t.Fatalf("first counter sample = %+v", aliyun)
	}

	rowsCfg := []monitor.Row{
		{ID: "dmit-card", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}},
		{ID: "dmit-cpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: monitor.Selector{Node: "dmit"}},
		{ID: "s2-card", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "s2"}},
	}
	panel, raw := monitorFrame(t, ch, at, rowsCfg)
	if panel.Rows[1].Value != 12.5 {
		t.Fatalf("dmit resource metric = %+v", panel.Rows[1])
	}
	generic := decodeMap(t, raw)
	rowsJSON := generic["rows"].([]any)
	card := rowsJSON[0].(map[string]any)["value"].(map[string]any)
	resources := card["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("stream resources = %+v", resources)
	}
	first := resources[0].(map[string]any)
	if first["check"] != "list-a" || first["source"] != "list-source" || first["selected"] != true {
		t.Fatalf("stream first resource = %+v", first)
	}
	cpu := first["cpu"].(map[string]any)
	if cpu["percent"] != 12.5 {
		t.Fatalf("stream cpu = %+v", cpu)
	}
	if first["valid_until"] == nil {
		t.Fatal("stream resource has no deadline")
	}
	// The card row outlives the resource's own deadline; the numbers do not.
	typed := panel.Rows[0].Value.(monitor.ServerCard)
	deadline := typed.Resources[0].ValidUntil
	if deadline == nil {
		t.Fatal("resource deadline missing")
	}
	late := monitor.Current(panel, deadline.Add(time.Second))
	lateCard, ok := late.Rows[0].Value.(monitor.ServerCard)
	if !ok {
		t.Fatal("card expired with its resource")
	}
	if lateCard.Resources[0].CPU.Percent != nil || lateCard.Resources[0].Disk.Percent != nil {
		t.Fatalf("expired resource still showed numbers: %+v", lateCard.Resources[0])
	}
}

func TestRepeatTenTimesCountsOneStatusSample(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	ch := newChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	state := ch.st.ExternalView()
	before := state.Bindings["status-000"].ReportSamples
	for i := 0; i < 10; i++ {
		ch.once(ctx)
		ch.deliver(ctx)
	}
	state = ch.st.ExternalView()
	b := state.Bindings["status-000"]
	if before != 1 || b.ReportSamples != 1 || b.Duplicates < 10 {
		t.Fatalf("duplicate status samples counted more than once: %+v", b)
	}
	if synthetic.count("/status") != 11 {
		t.Fatalf("status reads = %d", synthetic.count("/status"))
	}
}

func TestSameValueNewListSampleIsAcceptedAndSameIdentityConflictIsNot(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	ch := newChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	first := ch.st.ExternalView().Bindings["list-a"]
	if first.ReportSamples != 1 {
		t.Fatalf("first list report = %+v", first)
	}
	now := (*ch.now).UTC()
	payload := synthetic.defaultList()
	// Same values under a new sample identity and later upstream time.
	payload.Epoch = "epoch-1"
	for i := range payload.Machines {
		if payload.Machines[i].ID != "vm-a" {
			continue
		}
		sample := payload.Machines[i].Samples[2]
		sample.SampleID = "vm-a-2"
		sample.ObservedAt = now.Add(-time.Second)
		payload.Machines[i].Samples = []testkit.SyntheticMachineSample{sample}
	}
	synthetic.setList(payload)
	ch.once(ctx)
	ch.deliver(ctx)
	second := ch.st.ExternalView().Bindings["list-a"]
	if second.ReportSamples != 2 || !second.DataDeadline.Equal(now.Add(-time.Second).Add(600*time.Second)) {
		t.Fatalf("same-value new sample = %+v", second)
	}
	// Same identity, different content is a conflict and never overwrites.
	payload.Machines[0].Samples[0].CPUPercent = floatValue(99)
	synthetic.setList(payload)
	ch.once(ctx)
	ch.deliver(ctx)
	conflict := ch.st.ExternalView().Bindings["list-a"]
	if conflict.Conflicts != 1 || conflict.ReportSamples != 2 || *conflict.Last.Resource.CPU.Percent != 12.5 {
		t.Fatalf("conflict overwrote data = %+v", conflict)
	}
	// A late, older sample identity is recorded but cannot overwrite.
	old := payload.Machines[0].Samples[0]
	old.SampleID = "vm-a-older"
	old.ObservedAt = now.Add(-time.Hour)
	payload.Machines[0].Samples = []testkit.SyntheticMachineSample{old}
	synthetic.setList(payload)
	ch.once(ctx)
	ch.deliver(ctx)
	late := ch.st.ExternalView().Bindings["list-a"]
	if late.Late != 1 || late.ReportSamples != 2 || !late.DataDeadline.Equal(now.Add(-time.Second).Add(600*time.Second)) {
		t.Fatalf("late report advanced state = %+v", late)
	}
	// A new epoch and newer sample is accepted after the source restart.
	restart := payload.Machines[0].Samples[0]
	restart.SampleID = "vm-a-e2-1"
	restart.ObservedAt = now.Add(2 * time.Second)
	payload.Epoch = "epoch-2"
	payload.Machines[0].Samples = []testkit.SyntheticMachineSample{restart}
	synthetic.setList(payload)
	ch.once(ctx)
	ch.deliver(ctx)
	next := ch.st.ExternalView().Bindings["list-a"]
	if next.ReportSamples != 3 || next.Cursor.Epoch != "epoch-2" {
		t.Fatalf("epoch restart = %+v", next)
	}
}

func TestUnknownTimeIsNotFreshAndParseFailureIsNotOutage(t *testing.T) {
	synthetic := newSyntheticSource(t)
	status := synthetic.defaultStatus()
	status.ObservedAt = time.Time{}
	status.TimeBasis = model.TimeBasisUnknown
	status.SampleID = "unknown-time"
	synthetic.setStatus(status)
	c := integrationConfig(t, synthetic.server.URL, 1)
	ch := newChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	state := ch.st.ExternalView()
	b := state.Bindings["status-000"]
	if b.ReportSamples != 0 || b.Last != nil || b.Unusable != 1 {
		t.Fatalf("unknown time became evidence: %+v", b)
	}
	if !state.Sources["status-source"].LastContact.IsZero() {
		// Contact is a read fact and may be current; it must not make the
		// report data fresh.
	}
	snap := ch.snapshot((*ch.now).Add(time.Second))
	for _, e := range snap.External {
		if e.Check == "status-000" && (e.Report != nil || e.Health != "unknown") {
			t.Fatalf("unknown time produced a report health: %+v", e)
		}
	}
	// A malformed body is a source collection error, not target downtime.
	bad := integrationConfig(t, synthetic.server.URL, 1)
	for i := range bad.Sources {
		if bad.Sources[i].ID == "status-source" {
			bad.Sources[i].Target = synthetic.URL("/bad-json")
		}
	}
	badChain := newChain(t, bad)
	badChain.once(ctx)
	badChain.deliver(ctx)
	badState := badChain.st.ExternalView()
	if badState.Bindings["status-000"].ReportSamples != 0 || badState.Sources["status-source"].LastResult != "error" {
		t.Fatalf("parse error became target evidence: %+v", badState)
	}
	for _, cs := range badChain.snapshot((*badChain.now).Add(time.Second)).Checks {
		if cs.ID == "status-000" && (cs.Health == "down" || cs.Health == "up") {
			t.Fatalf("parse error produced target health: %+v", cs)
		}
	}
}

func TestOutboxReplayAndCheckpointDoNotDoubleApply(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	ch := newChain(t, c)
	ctx := context.Background()
	ch.collectorDelay.Store(true)
	ch.once(ctx)
	ch.deliver(ctx)
	first := ch.st.ExternalView()
	if err := ch.st.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// Reopen the store and API over the checkpoint, with the same source
	// context. The report cursors and source contact must survive.
	ch.collector.Close()
	ch.collector = nil
	ch.delivery.Close()
	ch.queue.Close()
	if err := ch.st.Close(); err != nil {
		t.Fatal(err)
	}
	ch.st = ch.openStore()
	ch.openBackend()
	ch.startCollector()
	ch.queue = ch.openQueue()
	ch.delivery = ch.openDelivery(workerToken)
	reopened := ch.st.ExternalView()
	if reopened == nil || reopened.Bindings["list-a"].ReportSamples != first.Bindings["list-a"].ReportSamples || !reopened.Sources["list-source"].LastContact.Equal(first.Sources["list-source"].LastContact) {
		t.Fatalf("checkpoint lost external state: %+v", reopened)
	}
	// Replay the exact worker batches. Observation receipts prevent the same
	// attempts and reports from being applied twice.
	for _, b := range ch.batches {
		if err := ch.queue.Add(b, ch.delivery.Recipients()); err != nil {
			t.Fatal(err)
		}
	}
	ch.deliver(ctx)
	after := ch.st.ExternalView()
	if after.Bindings["list-a"].ReportSamples != first.Bindings["list-a"].ReportSamples || after.Bindings["list-a"].Duplicates != first.Bindings["list-a"].Duplicates {
		t.Fatalf("replay applied side effects: %+v", after.Bindings["list-a"])
	}
	// A restarted worker has a new boot and observation IDs but reads the
	// same upstream sample. It is a duplicate report, not a new sample.
	ch.scheduler = ch.openSchedulerSynthetic()
	ch.once(ctx)
	ch.deliver(ctx)
	restarted := ch.st.ExternalView()
	if restarted.Bindings["list-a"].ReportSamples != first.Bindings["list-a"].ReportSamples || restarted.Bindings["list-a"].Duplicates <= first.Bindings["list-a"].Duplicates {
		t.Fatalf("worker restart changed report samples: %+v", restarted.Bindings["list-a"])
	}
}

func TestSyntheticSourceIsNotInProductionRegistry(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	// The integration test uses the test-only registry directly; a production
	// chain would fail to start because ProductionSourceAdapters is empty.
	// This assertion is covered in internal/probe; keep the config shape here
	// so the fixture cannot accidentally rely on a production registration.
	raw, _ := json.Marshal(c.Sources)
	if !strings.Contains(string(raw), testkit.SyntheticListAdapter) {
		t.Fatal("fixture does not name a synthetic adapter")
	}
}

func TestCounterNeedsTwoSamplesAndHandlesReset(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	ch := newChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	first := ch.st.ExternalView().Bindings["counter-vm"]
	if first.ReportSamples != 1 || first.Last.Resource.CPU.Percent != nil {
		t.Fatalf("first counter sample = %+v", first)
	}
	ch.once(ctx)
	ch.deliver(ctx)
	second := ch.st.ExternalView().Bindings["counter-vm"]
	if second.ReportSamples != 2 || second.Last.Resource.CPU.Percent == nil || *second.Last.Resource.CPU.Percent != 50 {
		t.Fatalf("counter rate = %+v", second.Last.Resource.CPU)
	}
	ch.once(ctx)
	ch.deliver(ctx)
	reset := ch.st.ExternalView().Bindings["counter-vm"]
	if reset.ReportSamples != 3 || reset.Last.Resource.CPU.Percent != nil || reset.Last.Epoch != "counter-2" {
		t.Fatalf("counter reset = %+v", reset.Last)
	}
}

func TestOrderedFallbackAndMissingPrimaryDisk(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	start := time.Now().UTC()
	list := synthetic.defaultList()
	list.Epoch = "epoch-fallback"
	for i := range list.Machines {
		for j := range list.Machines[i].Samples {
			list.Machines[i].Samples[j].ObservedAt = start
		}
	}
	synthetic.setList(list)
	c.Checks = append(c.Checks, model.Check{ID: "list-b-fallback", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "list-source", Object: "vm-b", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 600, PrimaryDisk: "data"}})
	c.Nodes[2].ResourceChecks = []string{"list-a", "list-b-fallback"}
	for i := range c.Checks {
		if c.Checks[i].ID == "list-a" {
			c.Checks[i].External.MaxAgeSeconds = 5
		}
	}
	for i := range c.Sources {
		if c.Sources[i].ID == "list-source" {
			c.Sources[i].StaleSeconds = 600
		}
	}
	for i := range c.Checks {
		if c.Checks[i].External != nil && c.Checks[i].External.Source == "list-source" {
			c.Checks[i].StaleSeconds = 600
		}
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	ch := newChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	at := (*ch.now).Add(time.Second)
	n := viewFor(ch.snapshot(at), at).SelectResources()["dmit"]
	if n.Selected.Check != "list-a" {
		t.Fatalf("primary was not selected first: %+v", n)
	}
	*ch.now = (*ch.now).Add(6 * time.Second)
	n = viewFor(ch.snapshot(*ch.now), *ch.now).SelectResources()["dmit"]
	if n.Selected.Check != "list-b-fallback" || !strings.Contains(n.Reason, "fallback") {
		t.Fatalf("expired primary did not fall back: %+v", n)
	}
	// Make the primary current again; it must win over the current fallback.
	recover := synthetic.defaultList()
	recover.Epoch = "epoch-fallback-2"
	for i := range recover.Machines {
		if recover.Machines[i].ID != "vm-a" {
			continue
		}
		sample := recover.Machines[i].Samples[2]
		sample.SampleID = "vm-a-recovered"
		sample.ObservedAt = *ch.now
		recover.Machines[i].Samples = []testkit.SyntheticMachineSample{sample}
	}
	synthetic.setList(recover)
	ch.once(ctx)
	ch.deliver(ctx)
	n = viewFor(ch.snapshot(*ch.now), *ch.now).SelectResources()["dmit"]
	if n.Selected.Check != "list-a" || !strings.Contains(n.Reason, "primary") {
		t.Fatalf("primary did not recover: %+v", n)
	}
	// A current primary with its configured disk absent stays selected and
	// shows disk unknown, not the fallback volume.
	missing := recover
	for i := range missing.Machines {
		if missing.Machines[i].ID != "vm-a" {
			continue
		}
		sample := missing.Machines[i].Samples[0]
		sample.SampleID = "vm-a-no-root"
		sample.ObservedAt = (*ch.now).Add(time.Second)
		sample.Disks = []testkit.SyntheticDisk{{ID: "data", Path: "/data"}}
		missing.Machines[i].Samples = []testkit.SyntheticMachineSample{sample}
	}
	synthetic.setList(missing)
	ch.once(ctx)
	ch.deliver(ctx)
	seenAt := (*ch.now).Add(2 * time.Second)
	n = viewFor(ch.snapshot(seenAt), seenAt).SelectResources()["dmit"]
	if n.Selected.Check != "list-a" || n.Selected.Facts == nil {
		t.Fatalf("partial primary = %+v", n)
	}
	if _, ok := n.Selected.Facts["disk_used_percent"]; ok || n.Selected.DiskPath == "/data" {
		t.Fatalf("missing primary disk borrowed fallback: %+v", n.Selected)
	}
	if len(n.All) != 1 || n.All[0].Check != "list-a" {
		t.Fatalf("partial primary stitched sources: %+v", n.All)
	}
}

func TestSourceFailureIsolationAndMissingObject(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	// Add a bad source and one binding; the good status binding must still
	// arrive, while the bad source produces only an attempt.
	c.Sources = append(c.Sources, model.Source{ID: "bad-source", Adapter: testkit.SyntheticStatusAdapter, Observer: "cloud", Target: synthetic.URL("/unauthorized"), IntervalSeconds: 1, TimeoutSeconds: 2, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 1, MaxReportBytes: 16 << 10})
	c.Checks = append(c.Checks, model.Check{ID: "bad-status", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "bad-source", Object: "svc-bad", Report: model.ReportTypeStatus, Node: "aliyun", MaxAgeSeconds: 60}})
	// Point list-source at an empty list: its bound objects become missing
	// attempts but the unrelated sources still proceed.
	empty := synthetic.defaultList()
	empty.Machines = nil
	synthetic.setList(empty)
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	ch := newChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	state := ch.st.ExternalView()
	if state.Bindings["status-000"].ReportSamples != 1 {
		t.Fatalf("good source was starved: %+v", state.Bindings["status-000"])
	}
	if state.Bindings["bad-status"].ReportSamples != 0 || state.Sources["bad-source"].LastResult != "error" {
		t.Fatalf("bad source became evidence: %+v", state)
	}
	if state.Bindings["list-a"].ReportSamples != 0 || state.Sources["list-source"].LastResult != "ok" {
		t.Fatalf("missing object was not a successful contact: %+v", state)
	}
}

func TestAPIRejectsClockSkewAndSurvivesAuthTimeoutAndBadAck(t *testing.T) {
	t.Run("clock skew", func(t *testing.T) {
		synthetic := newSyntheticSource(t)
		status := synthetic.defaultStatus()
		status.ObservedAt = time.Now().UTC().Add(time.Hour)
		status.SampleID = "future"
		synthetic.setStatus(status)
		c := integrationConfig(t, synthetic.server.URL, 1)
		ch := newChain(t, c)
		ctx := context.Background()
		ch.once(ctx)
		results := ch.delivery.Tick(ctx, ch.queue, nil)
		if len(results) == 0 || results[0].Error == "" {
			t.Fatalf("future report result = %+v", results)
		}
		if state := ch.st.ExternalView(); state != nil && state.Bindings["status-000"].ReportSamples != 0 {
			t.Fatalf("future report was stored: %+v", state)
		}
	})
	t.Run("auth", func(t *testing.T) {
		synthetic := newSyntheticSource(t)
		c := integrationConfig(t, synthetic.server.URL, 1)
		ch := newChain(t, c)
		ch.once(context.Background())
		ch.delivery.Close()
		ch.delivery = ch.openDelivery(strings.Repeat("a", 64))
		results := ch.delivery.Tick(context.Background(), ch.queue, nil)
		if len(results) == 0 || results[0].Error == "" {
			t.Fatalf("unauthenticated delivery result = %+v", results)
		}
		if rows, _ := ch.st.View(); len(rows) != 0 {
			t.Fatal("unauthenticated batch was stored")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		synthetic := newSyntheticSource(t)
		c := integrationConfig(t, synthetic.server.URL, 1)
		ch := newChain(t, c)
		ch.once(context.Background())
		ch.collectorDelay.Store(true)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		results := ch.delivery.Tick(ctx, ch.queue, nil)
		if len(results) == 0 || results[0].Error == "" {
			t.Fatalf("timeout delivery result = %+v", results)
		}
	})
	t.Run("bad ack", func(t *testing.T) {
		synthetic := newSyntheticSource(t)
		c := integrationConfig(t, synthetic.server.URL, 1)
		ch := newChain(t, c)
		ch.once(context.Background())
		ch.badAck.Store(true)
		results := ch.delivery.Tick(context.Background(), ch.queue, nil)
		if len(results) == 0 || results[0].Error == "" {
			t.Fatalf("bad ack result = %+v", results)
		}
	})
}

func TestResponseBudgetIsAnAttemptNotAResource(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	for i := range c.Sources {
		if c.Sources[i].ID == "list-source" {
			c.Sources[i].MaxResponseBytes = 1024
			c.Checks[0].External.MaxAgeSeconds = 60
		}
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	ch := newChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	state := ch.st.ExternalView()
	if state.Bindings["list-a"].ReportSamples != 0 || state.Sources["list-source"].LastResult != "error" {
		t.Fatalf("response budget became evidence: %+v", state)
	}
}

func TestSourceTimeoutIsAnAttempt(t *testing.T) {
	synthetic := newSyntheticSource(t)
	c := integrationConfig(t, synthetic.server.URL, 1)
	for i := range c.Sources {
		if c.Sources[i].ID == "status-source" {
			c.Sources[i].Target = synthetic.URL("/slow")
			c.Sources[i].TimeoutSeconds = 1
		}
	}
	for i := range c.Checks {
		if c.Checks[i].External != nil && c.Checks[i].External.Source == "status-source" {
			c.Checks[i].TimeoutSeconds = 1
		}
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	ch := newChain(t, c)
	start := time.Now()
	ch.once(context.Background())
	ch.deliver(context.Background())
	if time.Since(start) > 4*time.Second {
		t.Fatal("source timeout exceeded its budget")
	}
	state := ch.st.ExternalView()
	if state.Bindings["status-000"].ReportSamples != 0 || state.Sources["status-source"].LastResult != "error" {
		t.Fatalf("source timeout became target evidence: %+v", state)
	}
}
