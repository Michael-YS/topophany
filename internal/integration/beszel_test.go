package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	beszelSystemID = "validationsys01"
	beszelRecordID = "validationrec03"
	beszelToken    = "synthetic.beszel.integration-token"
	beszelStats    = `{"cpu":12.5,"m":8,"mu":3,"mp":37.5,"d":100,"du":20,"dp":23.25,"efs":{"storage":{"d":200,"du":20}}}`
	beszelPrefix   = "/private-hub"
)

// This is a protocol fixture, not a substituted source adapter. All decoding,
// validation, HTTP policy and registration below use the shipping runtime.
type beszelHubFixture struct {
	server    *httptest.Server
	mu        sync.Mutex
	record    string
	created   time.Time
	stats     json.RawMessage
	status    string
	forceHTTP int
	reads     map[string]int
}

func newBeszelHubFixture(t *testing.T) *beszelHubFixture {
	t.Helper()
	t.Setenv("TOPO_BESZEL_INTEGRATION_TOKEN", beszelToken)
	h := &beszelHubFixture{record: beszelRecordID, created: time.Now().UTC().Truncate(time.Millisecond).Add(-45 * time.Second), stats: json.RawMessage(beszelStats), status: "down", reads: map[string]int{}}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != beszelToken || r.Header.Get("Accept") != "application/json" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("Beszel transport did not use the expected read-only authorization/media contract")
			http.Error(w, "invalid test request", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.reads[r.URL.Path]++
		record, created, stats, status, forced := h.record, h.created, append(json.RawMessage(nil), h.stats...), h.status, h.forceHTTP
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if forced != 0 {
			w.WriteHeader(forced)
			_, _ = w.Write([]byte(`{"message":"` + beszelToken + `"}`))
			return
		}
		var payload any
		switch r.URL.Path {
		case beszelPrefix + "/api/collections/system_stats/records":
			want := url.Values{"page": {"1"}, "perPage": {"1"}, "sort": {"-created,-id"}, "skipTotal": {"true"}, "filter": {`system = "validationsys01" && type = "1m"`}, "fields": {"id,system,type,created,stats"}}
			if !reflect.DeepEqual(r.URL.Query(), want) {
				t.Errorf("Beszel resource query = %v, want %v", r.URL.Query(), want)
			}
			payload = map[string]any{"page": 1, "perPage": 1, "totalItems": -1, "totalPages": -1, "items": []any{map[string]any{"id": record, "system": beszelSystemID, "type": "1m", "created": created.Format("2006-01-02 15:04:05.000Z"), "stats": stats}}}
		case beszelPrefix + "/api/collections/systems/records/" + beszelSystemID:
			if !reflect.DeepEqual(r.URL.Query(), url.Values{"fields": {"id,status,updated"}}) {
				t.Errorf("Beszel status query = %v", r.URL.Query())
			}
			// A fresh metadata edit intentionally accompanies a down status.
			// It must not create a fresh measured outage.
			payload = map[string]any{"id": beszelSystemID, "status": status, "updated": time.Now().UTC().Format(time.RFC3339Nano)}
		default:
			t.Errorf("unexpected Beszel endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("encode fixture: %v", err)
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *beszelHubFixture) setSample(id string, at time.Time, stats string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.record, h.created, h.stats = id, at, json.RawMessage(stats)
}

func (h *beszelHubFixture) setHTTP(status int) { h.mu.Lock(); h.forceHTTP = status; h.mu.Unlock() }

func (h *beszelHubFixture) count(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads[path]
}

func beszelIntegrationConfig(t testing.TB, base string) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Sources = []model.Source{{ID: "beszel-hub", Adapter: "beszel", Observer: "cloud", Target: base + beszelPrefix, CredentialEnv: "TOPO_BESZEL_INTEGRATION_TOKEN", IntervalSeconds: 60, TimeoutSeconds: 5, StaleSeconds: 180, MaxResponseBytes: 128 << 10, MaxPages: 2, MaxObjects: 1, MaxReportBytes: model.MaxReportBytes}}
	c.Checks = []model.Check{
		{ID: "beszel-resource", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "beszel-hub", Object: beszelSystemID, Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 180, PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: floatValue(90), MemoryPercentMax: floatValue(90), DiskPercentMax: floatValue(85)}}},
		{ID: "beszel-resource-mirror", Observer: "cloud", Kind: "external", External: &model.ExternalBinding{Source: "beszel-hub", Object: beszelSystemID, Report: model.ReportTypeResource, Node: "s2", MaxAgeSeconds: 180, PrimaryDisk: "root"}},
		{ID: "beszel-status", Observer: "cloud", Service: "web", Kind: "external", External: &model.ExternalBinding{Source: "beszel-hub", Object: beszelSystemID, Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 180}},
	}
	for i := range c.Nodes {
		if c.Nodes[i].ID == "dmit" {
			c.Nodes[i].ResourceChecks = []string{"beszel-resource"}
		}
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func newBeszelChain(t testing.TB, c model.Config) *chain {
	t.Helper()
	now := time.Now().UTC()
	ch := &chain{t: t, cfg: c, now: &now, dir: t.TempDir()}
	ch.st = ch.openStore()
	ch.openBackend()
	ch.startCollector()
	t.Cleanup(ch.close)
	ch.queue = openBeszelQueue(t, filepath.Join(ch.dir, "worker-outbox"), c.Project)
	ch.delivery = ch.openDelivery(workerToken)
	ch.scheduler = beszelProductionScheduler(t, ch)
	return ch
}

func openBeszelQueue(t testing.TB, dir, project string) *outbox.Queue {
	t.Helper()
	q, err := outbox.OpenWithOptions(dir, project, outbox.Options{MaxBytes: 8 << 20, MaxRecords: 2000})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func beszelProductionScheduler(t testing.TB, ch *chain) *runner.Scheduler {
	t.Helper()
	registry := probe.ProductionSourceAdapters()
	if registry["beszel"] == nil {
		t.Fatal("shipping production registry does not contain Beszel")
	}
	for name := range registry {
		if strings.Contains(name, "synthetic") {
			t.Fatalf("test adapter %q entered shipping registry", name)
		}
	}
	s, err := runner.New(ch.cfg, "cloud", 1, func(context.Context, model.Config, model.Check) model.Observation {
		t.Error("external-only Beszel chain ran a native probe")
		return model.Observation{}
	}, func(o model.Observation) error { return ch.enqueue([]model.Observation{o}) })
	if err != nil {
		t.Fatal(err)
	}
	return s.WithSources(probe.NewSourceRuntime(registry), ch.enqueue, 1)
}

func beszelAssertNoSecrets(t testing.TB, values ...any) {
	t.Helper()
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{beszelToken, "TOPO_BESZEL_INTEGRATION_TOKEN"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("source credential escaped into observations, persisted data or monitor output")
			}
		}
	}
}

func TestBeszelProductionRegistryThroughRunnerOutboxAPIStoreAndMonitor(t *testing.T) {
	hub := newBeszelHubFixture(t)
	ch := newBeszelChain(t, beszelIntegrationConfig(t, hub.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	state := ch.st.ExternalView()
	if state == nil || len(state.Bindings) != 3 {
		t.Fatalf("external state = %+v", state)
	}
	for _, id := range []string{"beszel-resource", "beszel-resource-mirror"} {
		b := state.Bindings[id]
		if b.ReportSamples != 1 || b.Last == nil || b.Last.Resource == nil {
			t.Fatalf("%s resource report = %+v", id, b)
		}
		r := b.Last
		if r.SampleID != beszelRecordID || r.TimeBasis != model.TimeBasisUpstreamSample || !r.ObservedAt.Equal(hub.created) || !b.DataDeadline.Equal(hub.created.Add(180*time.Second)) {
			t.Fatalf("Hub record identity/time lost: %+v", b)
		}
		if r.Resource.CPU.Percent == nil || *r.Resource.CPU.Percent != 12.5 || r.Resource.Memory.TotalBytes == nil || *r.Resource.Memory.TotalBytes != 8589934592 || r.Resource.Memory.UsedBytes == nil || *r.Resource.Memory.UsedBytes != 3221225472 {
			t.Fatalf("wrong values after real transport and durable delivery: %+v", r.Resource)
		}
		if len(r.Resource.Disks) != 2 || r.Resource.Disks[0].Percent == nil || *r.Resource.Disks[0].Percent != 23.25 || r.Resource.Disks[0].Path != "" {
			t.Fatalf("root disk semantics lost: %+v", r.Resource.Disks)
		}
	}
	status := state.Bindings["beszel-status"]
	if status.ReportSamples != 0 || status.Last != nil || status.Unusable != 1 || status.StatusDown != 0 || status.UpstreamLatencySamples != 0 || status.ConfirmedDown {
		t.Fatalf("untimed status became an outage/latency sample: %+v", status)
	}
	if state.Sources["beszel-hub"].LastContact.IsZero() || state.Sources["beszel-hub"].LastResult != "ok" {
		t.Fatalf("source contact = %+v", state.Sources["beszel-hub"])
	}
	if hub.count(beszelPrefix+"/api/collections/system_stats/records") != 1 || hub.count(beszelPrefix+"/api/collections/systems/records/"+beszelSystemID) != 1 {
		t.Fatal("duplicate bindings caused redundant Hub reads")
	}
	for _, batch := range ch.batches {
		for _, o := range batch.Observations {
			if o.Connectivity || o.Evidence != "reported" || o.External == nil {
				t.Fatalf("reported sample became measured connectivity: %+v", o)
			}
		}
	}
	at := time.Now().UTC().Add(time.Second)
	snap := ch.snapshot(at)
	selected := viewFor(snap, at).SelectResources()["dmit"].Selected
	// The normalized report keeps Path empty. The shared view deliberately
	// displays the explicit disk ID when no mount path was supplied.
	if selected.Check != "beszel-resource" || selected.Source != "beszel-hub" || selected.Health != "up" || selected.Facts["cpu_used_percent"] != "12.50" || selected.Facts["memory_total_bytes"] != "8589934592" || selected.DiskPath != "root" {
		t.Fatalf("view selection = %+v", selected)
	}
	for _, v := range snap.External {
		if v.Check == "beszel-status" && (v.Report != nil || v.Health != "unknown") {
			t.Fatalf("status metadata appeared as fresh health: %+v", v)
		}
	}
	panel, raw := monitorFrame(t, ch, at, []monitor.Row{{ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}}, {ID: "cpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: monitor.Selector{Node: "dmit"}}})
	if panel.Rows[1].Value != 12.5 {
		t.Fatalf("monitor CPU = %+v", panel.Rows[1])
	}
	card, ok := panel.Rows[0].Value.(monitor.ServerCard)
	if !ok || len(card.Resources) != 1 || card.Resources[0].CPU.Percent == nil || *card.Resources[0].CPU.Percent != 12.5 || card.Resources[0].Disk.Percent == nil || *card.Resources[0].Disk.Percent != 23.25 || card.Resources[0].Disk.Path != "root" || card.Resources[0].ValidUntil == nil {
		t.Fatalf("monitor card = %+v", panel.Rows[0].Value)
	}
	resource := card.Resources[0]
	// Data freshness stays bound to the Hub sample. The display additionally
	// observes the shorter snapshot lease; these are different deadlines.
	if resource.SampledAt == nil || !resource.SampledAt.Equal(hub.created) || !resource.DataDeadline.Equal(hub.created.Add(180*time.Second)) || !resource.ValidUntil.Equal(selected.ValidUntil) || !resource.ValidUntil.Equal(at.Add(model.SnapshotLifetime)) {
		t.Fatalf("monitor lost sample or display deadlines: %+v", resource)
	}
	beszelAssertNoSecrets(t, ch.batches, state, snap, decodeMap(t, raw))
}

func TestBeszelRepeatedNewConflictingAndLateSamplesKeepTheirOwnTime(t *testing.T) {
	hub := newBeszelHubFixture(t)
	ch := newBeszelChain(t, beszelIntegrationConfig(t, hub.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	first := ch.st.ExternalView().Bindings["beszel-resource"]
	// A later receipt must not replace the first acceptance time. Keeping
	// the collector clock fixed would let an accidental rewrite pass this
	// assertion; advancing it still leaves the worker sample within the
	// ordinary ingestion window and the original resource lifetime.
	*ch.now = first.LastAcceptedAt.Add(time.Minute)
	ch.once(ctx)
	ch.deliver(ctx)
	repeated := ch.st.ExternalView().Bindings["beszel-resource"]
	if repeated.ReportSamples != 1 || repeated.Duplicates != 1 || !repeated.DataDeadline.Equal(first.DataDeadline) || !repeated.LastAcceptedAt.Equal(first.LastAcceptedAt) {
		t.Fatalf("identical fetch refreshed the sample: %+v", repeated)
	}
	newTime := hub.created.Add(20 * time.Second)
	hub.setSample("validationrec04", newTime, beszelStats)
	ch.once(ctx)
	ch.deliver(ctx)
	next := ch.st.ExternalView().Bindings["beszel-resource"]
	if next.ReportSamples != 2 || next.Last.SampleID != "validationrec04" || !next.DataDeadline.Equal(newTime.Add(180*time.Second)) {
		t.Fatalf("new sample with equal values was discarded: %+v", next)
	}
	hub.setSample("validationrec04", newTime, strings.Replace(beszelStats, `"cpu":12.5`, `"cpu":99`, 1))
	ch.once(ctx)
	ch.deliver(ctx)
	conflict := ch.st.ExternalView().Bindings["beszel-resource"]
	if conflict.Conflicts != 1 || conflict.ReportSamples != 2 || *conflict.Last.Resource.CPU.Percent != 12.5 || !conflict.DataDeadline.Equal(next.DataDeadline) {
		t.Fatalf("conflicting ID overwrote accepted evidence: %+v", conflict)
	}
	hub.setSample("validationrec02", newTime.Add(-time.Hour), beszelStats)
	ch.once(ctx)
	ch.deliver(ctx)
	late := ch.st.ExternalView().Bindings["beszel-resource"]
	if late.Late != 1 || late.ReportSamples != 2 || late.Last.SampleID != "validationrec04" || !late.DataDeadline.Equal(next.DataDeadline) {
		t.Fatalf("late response refreshed current data: %+v", late)
	}
}

func TestBeszelUnauthorizedIsSourceFailureAndDoesNotRefreshData(t *testing.T) {
	hub := newBeszelHubFixture(t)
	ch := newBeszelChain(t, beszelIntegrationConfig(t, hub.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	before := ch.st.ExternalView()
	hub.setHTTP(http.StatusUnauthorized)
	ch.once(ctx)
	ch.deliver(ctx)
	after := ch.st.ExternalView()
	s := after.Sources["beszel-hub"]
	if s.LastResult != "error" || s.LastCode != "beszel_auth" || !s.LastContact.Equal(before.Sources["beszel-hub"].LastContact) {
		t.Fatalf("authentication failure refreshed source contact: %+v", s)
	}
	b := after.Bindings["beszel-resource"]
	if b.ReportSamples != 1 || !b.DataDeadline.Equal(before.Bindings["beszel-resource"].DataDeadline) || !reflect.DeepEqual(b.Last, before.Bindings["beszel-resource"].Last) {
		t.Fatalf("authentication failure changed resource evidence: %+v", b)
	}
	for _, v := range ch.snapshot(time.Now().UTC().Add(time.Second)).Checks {
		if v.ID == "beszel-status" && (v.Health == "down" || v.Health == "up") {
			t.Fatalf("Hub auth error became target availability: %+v", v)
		}
	}
	last := ch.batches[len(ch.batches)-1]
	for _, o := range last.Observations {
		if o.Connectivity || o.External == nil || o.External.Contact || o.External.Report != nil || o.Code != "beszel_auth" {
			t.Fatalf("bad auth diagnostic: %+v", o)
		}
	}
	beszelAssertNoSecrets(t, ch.batches, after)
}

func TestBeszelCheckpointOutboxReopenReplayAndExpiry(t *testing.T) {
	hub := newBeszelHubFixture(t)
	ch := newBeszelChain(t, beszelIntegrationConfig(t, hub.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	// Close and reopen the actual on-disk outbox before its first delivery.
	if err := ch.queue.Close(); err != nil {
		t.Fatal(err)
	}
	ch.queue = openBeszelQueue(t, filepath.Join(ch.dir, "worker-outbox"), ch.cfg.Project)
	if len(ch.queue.Pending(ch.delivery.Recipients()[0], 10)) == 0 {
		t.Fatal("outbox restart lost undelivered Beszel samples")
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
	ch.queue = openBeszelQueue(t, filepath.Join(ch.dir, "worker-outbox"), ch.cfg.Project)
	ch.delivery = ch.openDelivery(workerToken)
	reopened := ch.st.ExternalView()
	if !reflect.DeepEqual(reopened, before) {
		t.Fatalf("checkpoint changed external state: before=%+v reopened=%+v", before, reopened)
	}
	for _, batch := range ch.batches {
		if err := ch.queue.Add(batch, ch.delivery.Recipients()); err != nil {
			t.Fatal(err)
		}
	}
	ch.deliver(ctx)
	if got := ch.st.ExternalView(); !reflect.DeepEqual(got, before) {
		t.Fatalf("exact worker-batch replay applied side effects: %+v", got)
	}
	ch.scheduler = beszelProductionScheduler(t, ch)
	ch.once(ctx)
	ch.deliver(ctx)
	restarted := ch.st.ExternalView().Bindings["beszel-resource"]
	if restarted.ReportSamples != 1 || restarted.Duplicates != 1 || !restarted.DataDeadline.Equal(before.Bindings["beszel-resource"].DataDeadline) {
		t.Fatalf("worker restart refreshed old upstream data: %+v", restarted)
	}
	deadline := restarted.DataDeadline
	// Publish a fresh frame shortly before the retained upstream deadline.
	// This isolates resource expiry while the ordinary row and publisher
	// leases remain valid, without modifying those leases in the fixture.
	at := deadline.Add(-5 * time.Second)
	panel, _ := monitorFrame(t, ch, at, []monitor.Row{{ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}}, {ID: "cpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: monitor.Selector{Node: "dmit"}}})
	expiredAt := deadline.Add(time.Second)
	fresh, ok := panel.Rows[0].Value.(monitor.ServerCard)
	if !ok || len(fresh.Resources) != 1 || fresh.Resources[0].CPU.Percent == nil || fresh.Resources[0].Memory.Percent == nil || fresh.Resources[0].Disk.Percent == nil || fresh.Resources[0].ValidUntil == nil || !fresh.Resources[0].ValidUntil.Equal(deadline) || panel.Rows[1].Value != 12.5 {
		t.Fatalf("fixture did not expose fresh resources before their deadline: %+v", panel)
	}
	if !expiredAt.Before(panel.PublisherExpiresAt) || !expiredAt.Before(panel.Rows[0].ValidUntil) {
		t.Fatal("resource-expiry check would expire its publisher or parent card")
	}
	late := monitor.Current(panel, expiredAt)
	card, ok := late.Rows[0].Value.(monitor.ServerCard)
	if late.Lifecycle != "running" || !ok || len(card.Resources) != 1 {
		t.Fatalf("host card disappeared with expired Beszel data: %+v", late.Rows[0])
	}
	resource := card.Resources[0]
	if resource.CPU.Percent != nil || resource.Memory.Percent != nil || resource.Disk.Percent != nil || resource.Freshness != "stale" || resource.Health != "unknown" || resource.Check != "beszel-resource" || !resource.DataDeadline.Equal(deadline) {
		t.Fatalf("expired Beszel values survived monitor deadline: %+v", card.Resources[0])
	}
	if late.Rows[1].Value != nil || late.Rows[1].Reason != "sample_expired" {
		t.Fatalf("scalar resource metric survived the same deadline: %+v", late.Rows[1])
	}
	for _, v := range ch.snapshot(deadline.Add(time.Second)).External {
		if v.Check == "beszel-resource" && v.Health == "up" {
			t.Fatalf("expired sample still considered up: %+v", v)
		}
	}
}

func TestBeszelProductionTransportRejectsRedirectAndOversizedBody(t *testing.T) {
	t.Setenv("TOPO_BESZEL_INTEGRATION_TOKEN", beszelToken)
	var escaped atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1); w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(sink.Close)
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }, "beszel_http"},
		{"oversized-body", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
		}, "response_budget"},
		{"wrong-content-type", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[]}`))
		}, "beszel_content_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			c := beszelIntegrationConfig(t, server.URL)
			s := c.Sources[0]
			s.MaxResponseBytes = 1024
			rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
			got, err := rt.Collect(context.Background(), c, s, []source.Request{{Object: beszelSystemID, Reports: []string{model.ReportTypeResource}}})
			if err == nil || source.Code(err) != tc.want || len(got) != 0 {
				t.Fatalf("real HTTP refusal = %+v %v (want %s)", got, err, tc.want)
			}
		})
	}
	if escaped.Load() != 0 {
		t.Fatalf("%d redirects escaped the configured origin", escaped.Load())
	}
}

func TestBeszelProductionTransportHonorsContextCancellation(t *testing.T) {
	t.Setenv("TOPO_BESZEL_INTEGRATION_TOKEN", beszelToken)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	c := beszelIntegrationConfig(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := probe.NewSourceRuntime(probe.ProductionSourceAdapters()).Collect(ctx, c, c.Sources[0], []source.Request{{Object: beszelSystemID, Reports: []string{model.ReportTypeResource}}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach local Hub fixture")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled HTTP request succeeded")
		}
		if strings.Contains(err.Error(), beszelToken) {
			t.Fatal("credential reflected in cancellation error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("source transport ignored context cancellation")
	}
}
