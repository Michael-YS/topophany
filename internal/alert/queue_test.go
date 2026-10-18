package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ledger"
)

func TestMaximumAlertQueueCanAlwaysRestart(t *testing.T) {
	snap, changes := failing(t)
	name := strings.Repeat("<&\"", 1365)
	snap.Topology.Observers[0].Name = name
	for i := range snap.Topology.Checks {
		snap.Topology.Checks[i].Name = name
	}
	if err := config.PresentationBounds(snap.Topology); err != nil {
		t.Fatal("fixture is not a valid presentation", err)
	}
	n := &Notifier{Path: filepath.Join(t.TempDir(), "alert-queue.json"), hooks: map[string]*hook{}, queues: map[string]*queue{}}
	for i := range maxWebhooks {
		id := fmt.Sprintf("hook-%d", i)
		n.hooks[id] = &hook{Webhook: Webhook{ID: id}, wake: make(chan struct{}, 1)}
		n.queues[id] = &queue{}
	}
	batch := []ledger.Change{}
	for i := range queueSize {
		change := changes[0]
		if i%2 != 0 {
			change.Kind = "closed"
			change.Record.Ended = "recovered"
			change.Record.Closed = snap.GeneratedAt
		}
		batch = append(batch, change)
	}
	n.Observe(snap, batch, false)
	raw, err := os.ReadFile(n.Path)
	if err != nil || len(raw) > maxBytes || n.unwritten {
		t.Fatal("saved queue exceeds its restart budget", len(raw), err)
	}
	restarted := &Notifier{Path: n.Path}
	if err := restarted.Load(); err != nil {
		t.Fatal("saved maximum queue cannot restart", err)
	}
	for id, q := range restarted.queues {
		if len(q.Waiting) != queueSize || q.Dropped != 0 {
			t.Fatal("valid events were lost", id, len(q.Waiting), q.Dropped)
		}
		for _, item := range q.Waiting {
			if len(item.Event.Title) > 253 || len(item.Event.Text) > maxText+3 {
				t.Fatal("unclipped event persisted")
			}
			req, err := (Webhook{Format: "json", URL: "http://127.0.0.1"}).request(context.Background(), item.Event, "lab", "primary")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(req.Body)
			req.Body.Close()
			var delivered Event
			if err := json.Unmarshal(body, &delivered); err != nil || delivered.Title != item.Event.Title || delivered.Text != item.Event.Text {
				t.Fatal("persisted and delivered presentation differ", err)
			}
		}
	}
}

func TestOversizedAlertDoesNotEvictAnotherObserver(t *testing.T) {
	snap, changes := failing(t)
	n := &Notifier{hooks: map[string]*hook{"ops": {Webhook: Webhook{ID: "ops"}, wake: make(chan struct{}, 1)}},
		queues: map[string]*queue{"ops": {Waiting: []queued{{Observer: "cloud", Event: Event{Title: "keep"}}}}}}
	changes[0].Record.Summary = strings.Repeat("<", 4000)
	n.Observe(snap, changes, false)
	q := n.queues["ops"]
	if len(q.Waiting) != 1 || q.Waiting[0].Observer != "cloud" || q.Dropped != 1 {
		t.Fatal("oversized alert displaced another observer", q)
	}
}

func TestQueueWriterPreservesReadableFileOnBudgetFailure(t *testing.T) {
	n := &Notifier{Path: filepath.Join(t.TempDir(), "alert-queue.json"), queues: map[string]*queue{}}
	n.save()
	before, _ := os.ReadFile(n.Path)
	n.queues["bad"] = &queue{Waiting: []queued{{Event: Event{Title: strings.Repeat("x", maxBytes)}}}}
	n.save()
	after, err := os.ReadFile(n.Path)
	if err != nil || string(after) != string(before) || !n.unwritten {
		t.Fatal("oversized write replaced the last readable queue", err)
	}
	if err := (&Notifier{Path: n.Path}).Load(); err != nil {
		t.Fatal("queue cannot restart after a rejected write", err)
	}
}

func TestLegacyQueueIsBoundedBeforeBeingSavedAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alert-queue.json")
	legacy := saved{Schema: 1, Webhooks: map[string]*queue{"ops": {Waiting: []queued{
		{Observer: "home", Event: Event{Title: strings.Repeat("<", 4096), Text: strings.Repeat("&", 4096)}},
		{Observer: "cloud", Event: Event{Title: "keep"}},
	}}}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	n := &Notifier{Path: path}
	if err := n.Load(); err != nil {
		t.Fatal(err)
	}
	n.save()
	reloaded := &Notifier{Path: path}
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	q := reloaded.queues["ops"]
	if len(q.Waiting) != 2 || q.Waiting[0].Observer != "home" || q.Waiting[1].Observer != "cloud" || len(q.Waiting[0].Event.Title) > 253 || len(q.Waiting[0].Event.Text) > maxText+3 {
		t.Fatal("legacy queue was not normalized without reordering observers")
	}
}

// What waits for a webhook is kept: a collector that stops with alerts it
// could not send sends them when it starts again. The file keeps no
// webhook's URL or bearer.
func TestAlertsWaitAcrossRestarts(t *testing.T) {
	snap, changes := failing(t)
	path := filepath.Join(t.TempDir(), "alert-queue.json")
	down := newEndpoint(t, 503, 503, 503, 503, 503, 503, 503, 503, 503, 503)
	hook := Webhook{ID: "ops", Format: "json", URL: down.srv.URL + "/hook?key=hidden", Bearer: "secret-bearer"}
	l := &logs{}
	first := &Notifier{Project: "lab", Instance: "primary", Log: l.log, Path: path, Backoff: func(int) time.Duration { return time.Hour }}
	if e := first.Load(); e != nil {
		t.Fatal(e)
	}
	first.Configure(Config{Schema: 1, Webhooks: []Webhook{hook}})
	defer first.Close()
	first.Observe(snap, changes, false)
	// A request reaching the endpoint does not mean its response has been
	// processed. Wait for the failed attempt to be persisted before stopping
	// delivery; closing earlier can cancel it before Tries is incremented.
	wait(t, func() bool {
		b, _ := os.ReadFile(path)
		return strings.Contains(string(b), `"tries":1`)
	})
	first.Close()
	b, e := os.ReadFile(path)
	if e != nil || !strings.Contains(string(b), "incident.opened") || !strings.Contains(string(b), `"tries":1`) {
		t.Fatal("what waits was not kept", string(b), e)
	}
	if strings.Contains(string(b), "hidden") || strings.Contains(string(b), "secret-bearer") || strings.Contains(string(b), "127.0.0.1") {
		t.Fatal("the queue keeps a webhook's URL or bearer", string(b))
	}

	up := newEndpoint(t)
	hook.URL = up.srv.URL
	second := &Notifier{Project: "lab", Instance: "primary", Log: l.log, Path: path}
	if e := second.Load(); e != nil {
		t.Fatal(e)
	}
	second.Configure(Config{Schema: 1, Webhooks: []Webhook{hook}})
	defer second.Close()
	wait(t, func() bool { return len(up.received()) == 1 })
	if got := up.received()[0]; !strings.Contains(string(got.body), "incident.opened") {
		t.Fatal(string(got.body))
	}
	if !strings.Contains(l.String(), "alerts: 1 wait for webhook ops from before; 0 sent and 0 dropped so far") {
		t.Fatal(l.String())
	}
	wait(t, func() bool {
		b, _ := os.ReadFile(path)
		return strings.Contains(string(b), `"waiting":[],"sent":1`)
	})
}

// A reload keeps what waits for a webhook by its ID and sends it as the
// webhook is now configured, drops what it no longer takes, and drops
// what waits for one no longer configured.
func TestReloadKeepsWhatWaitsByID(t *testing.T) {
	snap, changes := failing(t)
	down := newEndpoint(t, 503, 503, 503, 503, 503, 503)
	l := &logs{}
	n := &Notifier{Project: "lab", Instance: "primary", Log: l.log, Path: filepath.Join(t.TempDir(), "alert-queue.json"), Backoff: func(int) time.Duration { return time.Hour }}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{
		{ID: "moved", Format: "json", URL: down.srv.URL},
		{ID: "narrowed", Format: "json", URL: down.srv.URL, Events: []string{"incidents"}},
		{ID: "gone", Format: "json", URL: down.srv.URL},
	}})
	defer n.Close()
	n.Observe(snap, changes, false)
	wait(t, func() bool { return down.hits.Load() == 3 })
	moved, narrowed := newEndpoint(t), newEndpoint(t)
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{
		{ID: "moved", Format: "slack", URL: moved.srv.URL},
		{ID: "narrowed", Format: "json", URL: narrowed.srv.URL, Events: []string{"observers"}},
	}})
	wait(t, func() bool { return len(moved.received()) == 1 })
	if body := string(moved.received()[0].body); !strings.Contains(body, `"text"`) || strings.Contains(body, `"schema"`) {
		t.Fatal("a moved webhook was not sent what waited as it is now configured", body)
	}
	wait(t, func() bool { return strings.Contains(l.String(), "to webhook narrowed dropped: it no longer takes it") })
	if !strings.Contains(l.String(), "1 alerts for webhook gone dropped: it is no longer configured") {
		t.Fatal(l.String())
	}
	if narrowed.hits.Load() != 0 {
		t.Fatal("a webhook was sent what it no longer takes")
	}
}

// A queue file the collector cannot read stops it rather than being
// replaced, so no alerts are lost unseen.
func TestAnUnreadableAlertQueueStopsTheCollector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alert-queue.json")
	for _, bad := range []string{"{", `{"schema":2,"webhooks":{}}`, `{"schema":1,"webhooks":{},"extra":1}`} {
		if e := os.WriteFile(path, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if e := (&Notifier{Path: path}).Load(); e == nil || !strings.Contains(e.Error(), "move it aside") {
			t.Fatal(bad, e)
		}
	}
}
