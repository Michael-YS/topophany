package alert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
