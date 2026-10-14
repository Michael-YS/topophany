package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const workerToken = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func makeBatch(seq uint64) model.Batch {
	c := testkit.Config()
	return model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", seq, time.Now().UTC().Add(-time.Minute), "ok")}}
}
func queue(t *testing.T) *Queue {
	t.Helper()
	q, e := Open(t.TempDir(), "lab", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

// Pending gives the earliest sampled items still owed to a recipient, in
// that order, however the backlog is laid out and however much is asked.
func TestPendingIsTheEarliestInOrder(t *testing.T) {
	c := testkit.Config()
	q := queue(t)
	a, b := Recipient("a"), Recipient("b")
	start := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 150; i++ {
		v := testkit.Observation(c, "home-http", uint64(i+1), start.Add(time.Duration(i*37%150)*time.Second), "ok")
		if e := q.Add(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}}, []string{a, b}); e != nil {
			t.Fatal(e)
		}
	}
	all := q.Pending(a, 0)
	if len(all) != 150 {
		t.Fatal(len(all))
	}
	for i := 1; i < len(all); i++ {
		if !all[i-1].Batch.Observations[0].SampledAt.Before(all[i].Batch.Observations[0].SampledAt) {
			t.Fatal("pending items out of order at", i)
		}
	}
	// Acknowledged by a, still owed to b.
	if e := q.Ack([]string{all[0].ID, all[5].ID}, a); e != nil {
		t.Fatal(e)
	}
	owed := append(append([]Item{}, all[1:5]...), all[6:]...)
	for _, limit := range []int{1, 7, 100, 1000} {
		got := q.Pending(a, limit)
		want := owed[:min(limit, len(owed))]
		if len(got) != len(want) {
			t.Fatal(limit, len(got))
		}
		for i := range got {
			if got[i].ID != want[i].ID {
				t.Fatalf("limit %d: item %d is %s, want %s", limit, i, got[i].ID, want[i].ID)
			}
		}
	}
	if got := q.Pending(b, 1); got[0].ID != all[0].ID {
		t.Fatal("an acknowledgement by one recipient moved another's queue", got[0].ID)
	}
}
func TestDurablePartialAcknowledgementAndOwnership(t *testing.T) {
	dir := t.TempDir()
	q, e := Open(dir, "lab", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	a, b := Recipient("a"), Recipient("b")
	batch := makeBatch(1)
	if e = q.Add(batch, []string{a, b}); e != nil {
		t.Fatal(e)
	}
	original := batch.Observations[0].SampledAt
	batch.Observations[0].Facts["response_proof"] = "mutated"
	items := q.Pending(a, 1)
	if items[0].Batch.Observations[0].Facts["response_proof"] == "mutated" {
		t.Fatal("queue aliases caller")
	}
	items[0].Batch.Observations[0].Facts["response_proof"] = "mutated_again"
	id := items[0].ID
	if e = q.Ack([]string{id}, a); e != nil {
		t.Fatal(e)
	}
	q.Close()
	q, e = Open(dir, "lab", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if len(q.Pending(a, 1)) != 0 || len(q.Pending(b, 1)) != 1 {
		t.Fatal("partial ACK not persisted")
	}
	v := q.Pending(b, 1)[0].Batch.Observations[0]
	if !v.SampledAt.Equal(original) || v.Facts["response_proof"] != "http_response" {
		t.Fatal("durable payload changed", v)
	}
	if e = q.Ack([]string{id}, b); e != nil {
		t.Fatal(e)
	}
	if n, size := q.Stats(); n != 0 || size != 0 {
		t.Fatal(n, size)
	}
	if e = q.Ack([]string{id}, b); e != nil {
		t.Fatal("duplicate ack not idempotent")
	}
}
func TestQueueBudgetsValidationAndCorruption(t *testing.T) {
	q := queue(t)
	r := Recipient("a")
	b := makeBatch(1)
	if e := q.Add(b, []string{r}); e != nil {
		t.Fatal(e)
	}
	if e := q.Add(b, []string{r}); e != nil {
		t.Fatal(e)
	}
	if n, _ := q.Stats(); n != 1 {
		t.Fatal("duplicate record")
	}
	for _, rs := range [][]string{nil, {"../../bad"}, {r, r}} {
		b := makeBatch(2)
		if e := q.Add(b, rs); e == nil {
			t.Fatal("invalid recipients")
		}
	}
	wrong := makeBatch(3)
	wrong.Project = "other"
	if e := q.Add(wrong, []string{r}); e == nil {
		t.Fatal("wrong project")
	}
	tiny, e := Open(t.TempDir(), "lab", 1024)
	if e != nil {
		t.Fatal(e)
	}
	defer tiny.Close()
	b = makeBatch(4)
	b.Observations[0].Detail = strings.Repeat("x", 1500)
	if e = tiny.Add(b, []string{r}); !errors.Is(e, ErrFull) {
		t.Fatal(e)
	}
	if n, _ := tiny.Stats(); n != 0 {
		t.Fatal("full queue silently accepted")
	}
	if _, e = Open(t.TempDir(), "lab", 1); e == nil {
		t.Fatal("invalid budget")
	}
	q.Close()
	if e = q.Add(makeBatch(5), []string{r}); e == nil {
		t.Fatal("closed queue accepted")
	}
	if e = q.Ack(nil, r); e == nil {
		t.Fatal("closed ack accepted")
	}
	for _, kind := range []string{"checksum", "project", "empty", "id", "recipient"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			q, e := Open(dir, "lab", 1<<20)
			if e != nil {
				t.Fatal(e)
			}
			q.Add(makeBatch(1), []string{r})
			v := q.Pending(r, 1)[0]
			q.Close()
			switch kind {
			case "checksum":
				v.SHA256 = "bad"
			case "project":
				v.Batch.Project = "foreign"
				v.SHA256 = itemHash(v)
			case "empty":
				v.Batch.Observations = nil
				v.SHA256 = itemHash(v)
			case "id":
				v.ID = "bad"
				v.SHA256 = itemHash(v)
			case "recipient":
				v.Pending = []string{"bad"}
				v.SHA256 = itemHash(v)
			}
			entries, _ := os.ReadDir(filepath.Join(dir, "pending"))
			raw, _ := json.Marshal(v)
			os.WriteFile(filepath.Join(dir, "pending", entries[0].Name()), raw, 0600)
			if q, e = Open(dir, "lab", 1<<20); e == nil {
				q.Close()
				t.Fatal("corrupt queue opened")
			}
		})
	}
}
func TestQuarantinePreservesEvidenceAndOtherRecipients(t *testing.T) {
	q := queue(t)
	a, b := Recipient("a"), Recipient("b")
	q.Add(makeBatch(1), []string{a, b})
	id := q.Pending(a, 1)[0].ID
	if e := q.Quarantine([]string{id}, a, "HTTP 409 revision mismatch\x1b[2J"); e != nil {
		t.Fatal(e)
	}
	if len(q.Pending(a, 1)) != 0 || len(q.Pending(b, 1)) != 1 {
		t.Fatal("quarantine damaged other destination")
	}
	data, e := os.ReadFile(filepath.Join(q.dir, "quarantine", id+"-"+a+".json"))
	if e != nil || !strings.Contains(string(data), "revision mismatch") || strings.Contains(string(data), "\\u001b") {
		t.Fatal(e, string(data))
	}
	q.Ack([]string{id}, b)
	if n, size := q.Stats(); n != 0 || size == 0 {
		t.Fatal("quarantine not included in budget", n, size)
	}
	if !strings.Contains(q.String(), "including quarantine") {
		t.Fatal(q.String())
	}
}
func TestConcurrentQueueAppend(t *testing.T) {
	q := queue(t)
	r := Recipient("destination")
	var wg sync.WaitGroup
	for i := 1; i <= 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if e := q.Add(makeBatch(uint64(n)), []string{r}); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if n, _ := q.Stats(); n != 32 {
		t.Fatal(n)
	}
	if len(q.Pending(r, 7)) != 7 {
		t.Fatal("limit ignored")
	}
	if _, e := Open(q.dir, "lab", 1<<20); e == nil {
		t.Fatal("two writers allowed")
	}
}
func collector(t *testing.T, c model.Config, up *atomic.Bool) (*httptest.Server, *store.Store) {
	t.Helper()
	s, e := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	a := api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(workerToken), Role: "worker", Observer: "home"}}}
	h := api.New(c, "collector", s, a).Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if up != nil && !up.Load() {
			http.Error(w, "temporarily unavailable", 503)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}
func TestTwoCollectorsOutageRestartBackfillAndDeduplication(t *testing.T) {
	c := testkit.Config()
	var up atomic.Bool
	first, s1 := collector(t, c, nil)
	second, s2 := collector(t, c, &up)
	p := client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: first.URL, Token: workerToken}, {URL: second.URL, Token: workerToken}}}
	d, e := NewDelivery(p, config.Revision(c))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	q := queue(t)
	b := makeBatch(1)
	if e = q.Add(b, d.Recipients()); e != nil {
		t.Fatal(e)
	}
	results := d.Tick(context.Background(), q, nil)
	if results[0].Accepted != 1 || results[1].Error == "" {
		t.Fatal(results)
	}
	rows, _ := s1.View()
	if len(rows) != 1 {
		t.Fatal("healthy collector blocked by failed replica")
	}
	if rows, _ := s2.View(); len(rows) != 0 {
		t.Fatal(rows)
	}
	q.Close()
	q, e = Open(q.dir, "lab", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	up.Store(true)
	later := time.Now().Add(time.Minute)
	d.Now = func() time.Time { return later }
	results = d.Tick(context.Background(), q, nil)
	if results[1].Accepted != 1 {
		t.Fatal(results)
	}
	rows, _ = s2.View()
	if len(rows) != 1 || !rows[0].SampledAt.Equal(b.Observations[0].SampledAt) || rows[0].ReceivedAt.Before(rows[0].SampledAt) {
		t.Fatal("backfill clock violation", rows)
	}
	if n, _ := q.Stats(); n != 0 {
		t.Fatal("all acknowledged still pending")
	}
	q.Add(b, d.Recipients())
	d.Tick(context.Background(), q, nil)
	for _, s := range []*store.Store{s1, s2} {
		rows, _ := s.View()
		if len(rows) != 1 {
			t.Fatal("at-least-once delivery duplicated observation")
		}
	}
}
func TestPermanentMismatchQuarantineDoesNotLoseHealthyCopy(t *testing.T) {
	c := testkit.Config()
	good, s := collector(t, c, nil)
	c = testkit.Config()
	c.Checks[0].HTTP.Status = []int{401}
	bad, _ := collector(t, c, nil)
	p := client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: good.URL, Token: workerToken}, {URL: bad.URL, Token: workerToken}}}
	d, e := NewDelivery(p, config.Revision(testkit.Config()))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	q := queue(t)
	q.Add(makeBatch(1), d.Recipients())
	r := d.Tick(context.Background(), q, nil)
	if r[0].Accepted != 1 || r[1].Quarantined != 1 {
		t.Fatal(r)
	}
	if n, size := q.Stats(); n != 0 || size == 0 {
		t.Fatal(n, size)
	}
	if rows, _ := s.View(); len(rows) != 1 {
		t.Fatal(rows)
	}
}
func TestInvalidACKRetainsDataAndBackoff(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(202)
		w.Write([]byte(`{"project":"wrong","revision":"old","accepted":1}`))
	}))
	defer s.Close()
	d, e := NewDelivery(client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: s.URL, Token: workerToken}}}, makeBatch(1).Revision)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	q := queue(t)
	q.Add(makeBatch(1), d.Recipients())
	now := time.Now()
	d.Now = func() time.Time { return now }
	d.Tick(context.Background(), q, nil)
	d.Tick(context.Background(), q, nil)
	if hits.Load() != 1 {
		t.Fatal("backoff ignored")
	}
	if n, _ := q.Stats(); n != 1 {
		t.Fatal("invalid ACK erased payload")
	}
	now = now.Add(time.Minute)
	d.Tick(context.Background(), q, nil)
	if hits.Load() != 2 {
		t.Fatal("did not retry")
	}
}
func TestDeliveryHeartbeatAndBatchLimit(t *testing.T) {
	c := testkit.Config()
	s, st := collector(t, c, nil)
	d, e := NewDelivery(client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: s.URL, Token: workerToken}}}, config.Revision(c))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	q := queue(t)
	for i := 1; i <= 101; i++ {
		if e = q.Add(makeBatch(uint64(i)), d.Recipients()); e != nil {
			t.Fatal(e)
		}
	}
	hb := &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: time.Now()}
	r := d.Tick(context.Background(), q, hb)
	if r[0].Accepted != 100 {
		t.Fatal(r)
	}
	r = d.Tick(context.Background(), q, hb)
	if r[0].Accepted != 1 {
		t.Fatal(r)
	}
	rows, beats := st.View()
	if len(rows) != 101 || beats["home"].Observer != "home" {
		t.Fatal(len(rows), beats)
	}
	r = d.Tick(context.Background(), q, hb)
	if r[0].Error != "" {
		t.Fatal(r)
	}
}

// A worker upgraded with samples still queued delivers them as they were
// taken: the schema 1 items in a batch of their own, which a collector
// counts while their revision is its own, never merged with schema 2 ones.
func TestQueuedSchemaOneItemsTravelApart(t *testing.T) {
	c := testkit.Config()
	srv, st := collector(t, c, nil)
	d, e := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, config.Revision(c))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	q := queue(t)
	legacy := makeBatch(1)
	legacy.Schema = 0
	legacy.Observations[0].Contract, legacy.Observations[0].Revision = "", legacy.Revision
	for _, b := range []model.Batch{legacy, makeBatch(2)} {
		if e = q.Add(b, d.Recipients()); e != nil {
			t.Fatal(e)
		}
	}
	accepted := 0
	for i := 0; i < 2; i++ {
		r := d.Tick(context.Background(), q, nil)
		if r[0].Error != "" || r[0].Accepted != 1 {
			t.Fatal("each schema travels in its own batch", r)
		}
		accepted += r[0].Accepted
	}
	if rows, _ := st.View(); accepted != 2 || len(rows) != 2 {
		t.Fatal(accepted, len(rows))
	}
}

// Samples queued under a contract the collector no longer counts, as after
// a reload, are refused and set aside one per tick without backing off,
// and the samples behind them then travel together again.
func TestRefusedItemsDrainWithoutStallingDelivery(t *testing.T) {
	old := testkit.Config()
	next := testkit.Config()
	next.Checks[0].HTTP.Status = []int{204}
	srv, st := collector(t, next, nil)
	d, e := NewDelivery(client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, config.Revision(next))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	now := time.Now()
	d.Now = func() time.Time { return now } // no backoff can expire
	q := queue(t)
	sample := func(c model.Config, seq uint64) model.Batch {
		return model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", seq, now.Add(-time.Minute), "ok")}}
	}
	for seq := uint64(1); seq <= 3; seq++ {
		q.Add(sample(old, seq), d.Recipients())
	}
	for seq := uint64(4); seq <= 6; seq++ {
		q.Add(sample(next, seq), d.Recipients())
	}
	// Pending items come in no particular order, so refused and countable
	// ones interleave; every tick must still make progress.
	quarantined := 0
	for i := 0; i < 12; i++ {
		if n, _ := q.Stats(); n == 0 {
			break
		}
		r := d.Tick(context.Background(), q, nil)
		if r[0].Accepted == 0 && r[0].Quarantined == 0 && !strings.Contains(r[0].Error, "retrying individual") {
			t.Fatalf("tick %d made no progress: %+v", i, r[0])
		}
		quarantined += r[0].Quarantined
	}
	rows, _ := st.View()
	if n, _ := q.Stats(); n != 0 || quarantined != 3 || len(rows) != 3 {
		t.Fatalf("pending %d, quarantined %d, delivered %d", n, quarantined, len(rows))
	}
}
