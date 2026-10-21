package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/runner"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/view"
)

const (
	workerToken = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	viewerToken = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

type chain struct {
	t              testing.TB
	cfg            model.Config
	now            *time.Time
	dir            string
	st             *store.Store
	backend        *api.Server
	collector      *httptest.Server
	queue          *outbox.Queue
	delivery       *outbox.Delivery
	scheduler      *runner.Scheduler
	batches        []model.Batch
	collectorDelay atomic.Bool
	delayed        atomic.Bool
	badAck         atomic.Bool
	mu             sync.Mutex
}

func newChain(t testing.TB, c model.Config) *chain {
	t.Helper()
	now := time.Now().UTC()
	ch := &chain{t: t, cfg: c, now: &now, dir: t.TempDir()}
	ch.st = ch.openStore()
	ch.openBackend()
	ch.startCollector()
	t.Cleanup(ch.close)
	ch.queue = ch.openQueue()
	ch.delivery = ch.openDelivery(workerToken)
	ch.scheduler = ch.openSchedulerSynthetic()
	return ch
}

func (ch *chain) openStore() *store.Store {
	st, err := store.Open(ch.dir, store.Options{Revision: config.Revision(ch.cfg), Contracts: config.Contracts(ch.cfg), Stale: config.Stale(ch.cfg), Observers: observerIDs(ch.cfg), MaxHistory: 8, MaxReceipts: 5000, Retention: 24 * time.Hour, Now: func() time.Time { return *ch.now }})
	if err != nil {
		ch.t.Fatal(err)
	}
	return st
}

func (ch *chain) openBackend() {
	auth := api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(viewerToken), Role: "viewer"}, {TokenSHA256: secure.Hash(workerToken), Role: "worker", Observer: "cloud"}}}
	ch.backend = api.New(ch.cfg, "integration", ch.st, auth)
	ch.backend.Now = func() time.Time { return *ch.now }
}

func (ch *chain) startCollector() {
	handler := ch.backend.Handler()
	ch.collector = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && ch.collectorDelay.Swap(false) {
			ch.delayed.Store(true)
			time.Sleep(500 * time.Millisecond)
		}
		if r.Method == http.MethodPost && ch.badAck.Load() {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("not-json"))
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

func (ch *chain) openQueue() *outbox.Queue {
	q, err := outbox.OpenWithOptions(ch.t.TempDir(), ch.cfg.Project, outbox.Options{MaxBytes: 8 << 20, MaxRecords: 2000})
	if err != nil {
		ch.t.Fatal(err)
	}
	return q
}

func (ch *chain) openDelivery(token string) *outbox.Delivery {
	d, err := outbox.NewDelivery(client.Profile{Project: ch.cfg.Project, Servers: []client.Endpoint{{URL: ch.collector.URL, Token: token}}}, config.Revision(ch.cfg))
	if err != nil {
		ch.t.Fatal(err)
	}
	return d
}

func (ch *chain) openSchedulerSynthetic() *runner.Scheduler {
	if ch.backend == nil {
		ch.openBackend()
	}
	s, err := runner.New(ch.cfg, "cloud", 1, func(context.Context, model.Config, model.Check) model.Observation {
		ch.t.Fatal("native probe ran in an external-only chain")
		return model.Observation{}
	}, func(v model.Observation) error {
		return ch.enqueue([]model.Observation{v})
	})
	if err != nil {
		ch.t.Fatal(err)
	}
	s.WithSources(probe.NewSourceRuntime(testkit.SyntheticSourceAdapters()), func(obs []model.Observation) error {
		return ch.enqueue(obs)
	}, 4)
	return s
}

func (ch *chain) enqueue(obs []model.Observation) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	b := model.Batch{Schema: model.BatchSchema, Project: ch.cfg.Project, Revision: config.Revision(ch.cfg), Observations: append([]model.Observation(nil), obs...)}
	if err := ch.queue.Add(b, ch.delivery.Recipients()); err != nil {
		return err
	}
	ch.batches = append(ch.batches, b)
	return nil
}

func (ch *chain) once(ctx context.Context) {
	ch.t.Helper()
	if err := ch.scheduler.Once(ctx); err != nil {
		ch.t.Fatal(err)
	}
}

func (ch *chain) deliver(ctx context.Context) {
	ch.t.Helper()
	for i := 0; i < 20; i++ {
		results := ch.delivery.Tick(ctx, ch.queue, nil)
		for _, r := range results {
			if r.Error != "" {
				ch.t.Fatalf("delivery failed: %+v", r)
			}
		}
		if len(ch.queue.Pending(ch.delivery.Recipients()[0], 1)) == 0 {
			return
		}
	}
	ch.t.Fatal("outbox did not drain")
}

func (ch *chain) snapshot(at time.Time) model.Snapshot {
	ch.t.Helper()
	return ch.backend.MonitorSnapshot(at)
}

func (ch *chain) close() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.collector != nil {
		ch.collector.Close()
		ch.collector = nil
	}
	if ch.delivery != nil {
		ch.delivery.Close()
		ch.delivery = nil
	}
	if ch.queue != nil {
		ch.queue.Close()
		ch.queue = nil
	}
	if ch.st != nil {
		ch.st.Close()
		ch.st = nil
	}
}

func observerIDs(c model.Config) []string {
	out := []string{}
	for _, o := range c.Observers {
		out = append(out, o.ID)
	}
	return out
}

func viewFor(s model.Snapshot, at time.Time) view.View { return view.View{Snapshot: s, Now: at} }

func monitorFrame(t testing.TB, ch *chain, at time.Time, rows []monitor.Row) (monitor.Panel, []byte) {
	t.Helper()
	cfg := monitor.Config{Name: "integration", Source: "preview", Template: monitor.Template{Schema: 1, Title: "Topophany", Rows: rows}}
	snap := ch.snapshot(at)
	cfg.Project = snap.Project
	p := monitor.Build(cfg, monitor.Input{Snapshot: snap}, at)
	p.PublisherExpiresAt = at.Add(30 * time.Second)
	var buf strings.Builder
	if err := stream.WriteFrame(&buf, p); err != nil {
		t.Fatal(err)
	}
	return p, []byte(buf.String())
}

func decodeMap(t testing.TB, raw []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func externalChecks(c model.Config) []model.Check {
	out := []model.Check{}
	for _, x := range c.Checks {
		if x.Kind == "external" {
			out = append(out, x)
		}
	}
	return out
}
