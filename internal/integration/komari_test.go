package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/runner"
	"github.com/WhitenWhiten/topophany/internal/source"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const (
	komariObject = "12345678-1234-4234-8234-123456789abc"
	komariToken  = "synthetic.komari.integration-token"
	komariPrefix = "/private-monitor"
	komariStats  = `"cpu":{"usage":12.5},"ram":{"total":8589934592,"used":3221225472},"disk":{"total":107374182400,"used":21474836480}`
)

// The local server speaks Komari 1.6.0's recent-report protocol. The adapter,
// registry, transport, scheduler and delivery path are the production code.
type komariFixture struct {
	server    *httptest.Server
	mu        sync.Mutex
	at        time.Time
	stats     string
	forceHTTP int
	empty     bool
	reads     int
}

func newKomariFixture(t *testing.T) *komariFixture {
	t.Helper()
	t.Setenv("TOPO_KOMARI_INTEGRATION_TOKEN", komariToken)
	h := &komariFixture{at: time.Now().UTC().Add(-45 * time.Second), stats: komariStats}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != komariPrefix+"/api/recent/"+komariObject || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+komariToken || r.Header.Get("Accept") != "application/json" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("Komari request violated the read-only endpoint or credential contract")
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.reads++
		at, stats, forced, empty := h.at, h.stats, h.forceHTTP, h.empty
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if forced != 0 {
			w.WriteHeader(forced)
			_, _ = w.Write([]byte(`{"status":"error","message":"` + komariToken + `"}`))
			return
		}
		data := []json.RawMessage{}
		if !empty {
			// Reverse chronological order deliberately prevents an adapter
			// from treating the final array element as the newest sample.
			data = append(data, komariSample(at, stats), komariSample(at.Add(-10*time.Second), strings.Replace(komariStats, `"usage":12.5`, `"usage":99`, 1)))
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"status": "success", "message": "", "data": data}); err != nil {
			t.Errorf("encode Komari fixture: %v", err)
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}

func komariSample(at time.Time, stats string) json.RawMessage {
	return json.RawMessage(`{"uuid":"` + komariObject + `","updated_at":"` + at.Format(time.RFC3339Nano) + `",` + stats + `,"process":10,"message":"` + komariToken + `"}`)
}

func (h *komariFixture) setSample(at time.Time, stats string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.at, h.stats = at, stats
}

func (h *komariFixture) setHTTP(code int) { h.mu.Lock(); h.forceHTTP = code; h.mu.Unlock() }
func (h *komariFixture) setEmpty()        { h.mu.Lock(); h.empty = true; h.mu.Unlock() }
func (h *komariFixture) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads
}

func komariIntegrationConfig(t testing.TB, base string) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Sources = []model.Source{{ID: "komari-server", Adapter: "komari", Observer: "cloud", Target: base + komariPrefix, CredentialEnv: "TOPO_KOMARI_INTEGRATION_TOKEN", IntervalSeconds: 60, TimeoutSeconds: 5, StaleSeconds: 180, MaxResponseBytes: 128 << 10, MaxPages: 1, MaxObjects: 1, MaxReportBytes: model.MaxReportBytes}}
	c.Checks = []model.Check{
		{ID: "komari-resource", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "komari-server", Object: komariObject, Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 180, PrimaryDisk: "aggregate", Assertions: &model.ResourceAssertions{CPUPercentMax: floatValue(90), MemoryPercentMax: floatValue(90), DiskPercentMax: floatValue(85)}}},
		{ID: "komari-resource-mirror", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "komari-server", Object: komariObject, Report: model.ReportTypeResource, Node: "s2", MaxAgeSeconds: 180, PrimaryDisk: "aggregate"}},
	}
	for i := range c.Nodes {
		if c.Nodes[i].ID == "dmit" {
			c.Nodes[i].ResourceChecks = []string{"komari-resource"}
		}
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func newKomariChain(t testing.TB, c model.Config) *chain {
	t.Helper()
	now := time.Now().UTC()
	ch := &chain{t: t, cfg: c, now: &now, dir: t.TempDir()}
	ch.st = ch.openStore()
	ch.openBackend()
	ch.startCollector()
	t.Cleanup(ch.close)
	ch.queue = openKomariQueue(t, ch)
	ch.delivery = ch.openDelivery(workerToken)
	ch.scheduler = komariProductionScheduler(t, ch)
	return ch
}

func openKomariQueue(t testing.TB, ch *chain) *outbox.Queue {
	t.Helper()
	q, err := outbox.OpenWithOptions(filepath.Join(ch.dir, "worker-outbox"), ch.cfg.Project, outbox.Options{MaxBytes: 8 << 20, MaxRecords: 2000})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func komariProductionScheduler(t testing.TB, ch *chain) *runner.Scheduler {
	t.Helper()
	registry := probe.ProductionSourceAdapters()
	if registry["komari"] == nil {
		t.Fatal("Komari is missing from the shipping production registry")
	}
	if err := probe.NewSourceRuntime(registry).Validate(ch.cfg); err != nil {
		t.Fatalf("production source validation: %v", err)
	}
	s, err := runner.New(ch.cfg, "cloud", 1, func(context.Context, model.Config, model.Check) model.Observation {
		t.Error("external-only Komari chain ran a native probe")
		return model.Observation{}
	}, func(o model.Observation) error { return ch.enqueue([]model.Observation{o}) })
	if err != nil {
		t.Fatal(err)
	}
	return s.WithSources(probe.NewSourceRuntime(registry), ch.enqueue, 1)
}

func komariAssertNoSecrets(t testing.TB, values ...any) {
	t.Helper()
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{komariToken, "TOPO_KOMARI_INTEGRATION_TOKEN"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("Komari credential escaped into persisted data, observations or public output")
			}
		}
	}
}

func TestKomariProductionRegistryThroughRunnerOutboxAPIStoreAndMonitor(t *testing.T) {
	h := newKomariFixture(t)
	ch := newKomariChain(t, komariIntegrationConfig(t, h.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	state := ch.st.ExternalView()
	if state == nil || len(state.Bindings) != 2 {
		t.Fatalf("external state = %+v", state)
	}
	for _, id := range []string{"komari-resource", "komari-resource-mirror"} {
		b := state.Bindings[id]
		if b.ReportSamples != 1 || b.Last == nil || b.Last.Resource == nil {
			t.Fatalf("%s resource report = %+v", id, b)
		}
		r := b.Last
		if r.SampleID == "" || r.Object != komariObject || r.TimeBasis != model.TimeBasisUpstreamSample || !r.ObservedAt.Equal(h.at) || !b.DataDeadline.Equal(h.at.Add(180*time.Second)) {
			t.Fatalf("Komari report lost its object, sample identity or upstream time: %+v", b)
		}
		if r.Resource.CPU == nil || r.Resource.CPU.Percent == nil || *r.Resource.CPU.Percent != 12.5 || r.Resource.Memory == nil || r.Resource.Memory.TotalBytes == nil || *r.Resource.Memory.TotalBytes != 8589934592 || r.Resource.Memory.UsedBytes == nil || *r.Resource.Memory.UsedBytes != 3221225472 || r.Resource.Memory.Percent == nil || *r.Resource.Memory.Percent != 37.5 {
			t.Fatalf("resource values changed across transport and durable delivery: %+v", r.Resource)
		}
		if len(r.Resource.Disks) != 1 || r.Resource.Disks[0].ID != "aggregate" || r.Resource.Disks[0].Path != "" || r.Resource.Disks[0].Percent == nil || *r.Resource.Disks[0].Percent != 20 || r.Resource.Disks[0].TotalBytes == nil || *r.Resource.Disks[0].TotalBytes != 107374182400 || r.Resource.Disks[0].UsedBytes == nil || *r.Resource.Disks[0].UsedBytes != 21474836480 {
			t.Fatalf("whole-machine disk counters acquired false root/mount semantics: %+v", r.Resource.Disks)
		}
	}
	if h.count() != 1 || state.Sources["komari-server"].LastResult != "ok" || state.Sources["komari-server"].LastContact.IsZero() {
		t.Fatal("one object bound twice was not collected once with successful source contact")
	}
	for _, batch := range ch.batches {
		for _, o := range batch.Observations {
			if o.Connectivity || o.Evidence != "reported" || o.External == nil {
				t.Fatalf("Komari resources became measured availability: %+v", o)
			}
		}
	}
	at := time.Now().UTC().Add(time.Second)
	snap := ch.snapshot(at)
	selected := viewFor(snap, at).SelectResources()["dmit"].Selected
	if selected.Check != "komari-resource" || selected.Source != "komari-server" || selected.Health != "up" || selected.Facts["cpu_used_percent"] != "12.50" || selected.Facts["memory_total_bytes"] != "8589934592" || selected.DiskPath != "aggregate" {
		t.Fatalf("resource selection = %+v", selected)
	}
	panel, raw := monitorFrame(t, ch, at, []monitor.Row{{ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}}, {ID: "cpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: monitor.Selector{Node: "dmit"}}})
	if panel.Rows[1].Value != 12.5 {
		t.Fatalf("monitor CPU = %+v", panel.Rows[1])
	}
	card, ok := panel.Rows[0].Value.(monitor.ServerCard)
	if !ok || len(card.Resources) != 1 || card.Resources[0].CPU.Percent == nil || *card.Resources[0].CPU.Percent != 12.5 || card.Resources[0].Disk.Percent == nil || *card.Resources[0].Disk.Percent != 20 || card.Resources[0].Disk.Path != "aggregate" {
		t.Fatalf("monitor card = %+v", panel.Rows[0].Value)
	}
	r := card.Resources[0]
	if r.SampledAt == nil || !r.SampledAt.Equal(h.at) || !r.DataDeadline.Equal(h.at.Add(180*time.Second)) || r.ValidUntil == nil || !r.ValidUntil.Equal(selected.ValidUntil) {
		t.Fatalf("monitor resource lost sample/deadline semantics: %+v", r)
	}
	komariAssertNoSecrets(t, ch.batches, state, snap, decodeMap(t, raw))
}

func TestKomariRepeatedNewConflictingAndLateSamplesKeepUpstreamTime(t *testing.T) {
	h := newKomariFixture(t)
	ch := newKomariChain(t, komariIntegrationConfig(t, h.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	first := ch.st.ExternalView().Bindings["komari-resource"]
	*ch.now = first.LastAcceptedAt.Add(time.Minute)
	ch.once(ctx)
	ch.deliver(ctx)
	repeat := ch.st.ExternalView().Bindings["komari-resource"]
	if repeat.ReportSamples != 1 || repeat.Duplicates != 1 || !repeat.DataDeadline.Equal(first.DataDeadline) || !repeat.LastAcceptedAt.Equal(first.LastAcceptedAt) {
		t.Fatalf("fetching a cached report renewed its evidence: %+v", repeat)
	}
	newTime := h.at.Add(20 * time.Second)
	h.setSample(newTime, komariStats)
	ch.once(ctx)
	ch.deliver(ctx)
	next := ch.st.ExternalView().Bindings["komari-resource"]
	if next.ReportSamples != 2 || next.Last.SampleID == first.Last.SampleID || !next.DataDeadline.Equal(newTime.Add(180*time.Second)) {
		t.Fatalf("new upstream report with equal values was discarded: %+v", next)
	}
	h.setSample(newTime, strings.Replace(komariStats, `"usage":12.5`, `"usage":99`, 1))
	ch.once(ctx)
	ch.deliver(ctx)
	conflict := ch.st.ExternalView().Bindings["komari-resource"]
	if conflict.ReportSamples != 2 || conflict.Conflicts != 1 || *conflict.Last.Resource.CPU.Percent != 12.5 || !conflict.DataDeadline.Equal(next.DataDeadline) {
		t.Fatalf("same-time contradictory report replaced accepted evidence: %+v", conflict)
	}
	h.setSample(newTime.Add(-time.Hour), komariStats)
	ch.once(ctx)
	ch.deliver(ctx)
	late := ch.st.ExternalView().Bindings["komari-resource"]
	if late.ReportSamples != 2 || late.Late != 1 || late.Last.SampleID != next.Last.SampleID || !late.DataDeadline.Equal(next.DataDeadline) {
		t.Fatalf("older recent report replaced current evidence: %+v", late)
	}
}

func TestKomariSourceFailureAndEmptyHistoryDoNotRefreshResourceEvidence(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "unauthorized"
		if empty {
			name = "empty-recent-history"
		}
		t.Run(name, func(t *testing.T) {
			h := newKomariFixture(t)
			ch := newKomariChain(t, komariIntegrationConfig(t, h.server.URL))
			ctx := context.Background()
			ch.once(ctx)
			ch.deliver(ctx)
			before := ch.st.ExternalView()
			if empty {
				h.setEmpty()
			} else {
				h.setHTTP(http.StatusUnauthorized)
			}
			ch.once(ctx)
			ch.deliver(ctx)
			after := ch.st.ExternalView()
			b := after.Bindings["komari-resource"]
			if b.ReportSamples != 1 || !b.DataDeadline.Equal(before.Bindings["komari-resource"].DataDeadline) || !reflect.DeepEqual(b.Last, before.Bindings["komari-resource"].Last) || b.ConfirmedDown {
				t.Fatalf("missing data or source failure changed resource evidence: %+v", b)
			}
			if !empty {
				s := after.Sources["komari-server"]
				if s.LastResult != "error" || s.LastCode != "komari_auth" || !s.LastContact.Equal(before.Sources["komari-server"].LastContact) {
					t.Fatalf("authentication failure refreshed source contact: %+v", s)
				}
			}
			for _, o := range ch.batches[len(ch.batches)-1].Observations {
				if o.Connectivity || o.External == nil || o.External.Report != nil || !empty && (o.External.Contact || o.Code != "komari_auth") {
					t.Fatalf("source response created target availability: %+v", o)
				}
			}
			komariAssertNoSecrets(t, ch.batches, after, ch.snapshot(time.Now().UTC().Add(time.Second)))
		})
	}
}

func TestKomariCheckpointOutboxReplayAndResourceExpiry(t *testing.T) {
	h := newKomariFixture(t)
	ch := newKomariChain(t, komariIntegrationConfig(t, h.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	if err := ch.queue.Close(); err != nil {
		t.Fatal(err)
	}
	ch.queue = openKomariQueue(t, ch)
	if len(ch.queue.Pending(ch.delivery.Recipients()[0], 10)) == 0 {
		t.Fatal("outbox restart lost undelivered Komari samples")
	}
	ch.deliver(ctx)
	before := ch.st.ExternalView()
	if err := ch.st.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	ch.collector.Close()
	ch.collector = nil
	ch.delivery.Close()
	if err := ch.queue.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ch.st.Close(); err != nil {
		t.Fatal(err)
	}
	ch.st = ch.openStore()
	ch.openBackend()
	ch.startCollector()
	ch.queue = openKomariQueue(t, ch)
	ch.delivery = ch.openDelivery(workerToken)
	if !reflect.DeepEqual(ch.st.ExternalView(), before) {
		t.Fatal("checkpoint/reopen changed Komari source or binding state")
	}
	for _, batch := range ch.batches {
		if err := ch.queue.Add(batch, ch.delivery.Recipients()); err != nil {
			t.Fatal(err)
		}
	}
	ch.deliver(ctx)
	if !reflect.DeepEqual(ch.st.ExternalView(), before) {
		t.Fatal("replayed durable batches applied source/report side effects twice")
	}
	ch.scheduler = komariProductionScheduler(t, ch)
	ch.once(ctx)
	ch.deliver(ctx)
	b := ch.st.ExternalView().Bindings["komari-resource"]
	if b.ReportSamples != 1 || b.Duplicates != 1 || !b.DataDeadline.Equal(before.Bindings["komari-resource"].DataDeadline) {
		t.Fatalf("worker restart renewed cached report evidence: %+v", b)
	}
	at := b.DataDeadline.Add(-5 * time.Second)
	panel, _ := monitorFrame(t, ch, at, []monitor.Row{{ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}}, {ID: "cpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: monitor.Selector{Node: "dmit"}}})
	expiredAt := b.DataDeadline.Add(time.Second)
	card, ok := panel.Rows[0].Value.(monitor.ServerCard)
	if !ok || len(card.Resources) != 1 || card.Resources[0].ValidUntil == nil || !card.Resources[0].ValidUntil.Equal(b.DataDeadline) || card.Resources[0].CPU.Percent == nil || panel.Rows[1].Value != 12.5 {
		t.Fatalf("fixture did not expose resources before their actual data deadline: %+v", panel)
	}
	if !expiredAt.Before(panel.PublisherExpiresAt) || !expiredAt.Before(panel.Rows[0].ValidUntil) {
		t.Fatal("resource expiry was masked by expiration of its parent card or publisher")
	}
	late := monitor.Current(panel, expiredAt)
	card, ok = late.Rows[0].Value.(monitor.ServerCard)
	if !ok || len(card.Resources) != 1 || late.Lifecycle != "running" || card.Resources[0].CPU.Percent != nil || card.Resources[0].Memory.Percent != nil || card.Resources[0].Disk.Percent != nil || card.Resources[0].Freshness != "stale" || card.Resources[0].Health != "unknown" {
		t.Fatalf("expired resources retained numeric values: %+v", late.Rows[0])
	}
	if late.Rows[1].Value != nil || late.Rows[1].Reason != "sample_expired" {
		t.Fatalf("expired scalar resource retained a value: %+v", late.Rows[1])
	}
}

func TestKomariProductionTransportRefusalsAndUnsupportedStatus(t *testing.T) {
	t.Setenv("TOPO_KOMARI_INTEGRATION_TOKEN", komariToken)
	var escaped atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1); w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(sink.Close)
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }, "komari_http"},
		{"oversized-body", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
		}, "response_budget"},
		{"wrong-content-type", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`{"status":"success","message":"","data":[]}`))
		}, "komari_content_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			c := komariIntegrationConfig(t, server.URL)
			s := c.Sources[0]
			s.MaxResponseBytes = 1024
			got, err := probe.NewSourceRuntime(probe.ProductionSourceAdapters()).Collect(context.Background(), c, s, []source.Request{{Object: komariObject, Reports: []string{model.ReportTypeResource}}})
			if err == nil || source.Code(err) != tc.want || len(got) != 0 {
				t.Fatalf("production transport refusal = %+v %v (want %s)", got, err, tc.want)
			}
		})
	}
	if escaped.Load() != 0 {
		t.Fatal("production transport followed a redirect outside the configured source")
	}
	t.Run("status-rejected-before-read", func(t *testing.T) {
		h := newKomariFixture(t)
		c := komariIntegrationConfig(t, h.server.URL)
		got, err := probe.NewSourceRuntime(probe.ProductionSourceAdapters()).Collect(context.Background(), c, c.Sources[0], []source.Request{{Object: komariObject, Reports: []string{model.ReportTypeResource, model.ReportTypeStatus}}})
		if err == nil || source.Code(err) != "unsupported_report_type" || len(got) != 0 || h.count() != 0 {
			t.Fatalf("unsupported status request collected data: got=%+v err=%v reads=%d", got, err, h.count())
		}
	})
}

func TestKomariProductionTransportCancelsAnOutstandingRead(t *testing.T) {
	t.Setenv("TOPO_KOMARI_INTEGRATION_TOKEN", komariToken)
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	// Even a regression that drops the request context must not hang the
	// test server's cleanup after the explicit cancellation deadline fails.
	defer func() { close(release); server.Close() }()
	c := komariIntegrationConfig(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := probe.NewSourceRuntime(probe.ProductionSourceAdapters()).Collect(ctx, c, c.Sources[0], []source.Request{{Object: komariObject, Reports: []string{model.ReportTypeResource}}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Komari request did not reach the local fixture")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || strings.Contains(err.Error(), komariToken) {
			t.Fatalf("canceled read succeeded or reflected a credential: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Komari request ignored context cancellation")
	}
}
