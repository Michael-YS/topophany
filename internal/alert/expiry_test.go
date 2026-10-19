package alert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestExpiredAlertIsNotSentAfterRestart(t *testing.T) {
	snap, changes := failing(t)
	event := incidentEvent(view.View{Snapshot: snap, Now: snap.GeneratedAt}, "incident.opened", changes[0].Record, snap.GeneratedAt)
	path := filepath.Join(t.TempDir(), "queue.json")
	data, err := json.Marshal(saved{Schema: 1, Webhooks: map[string]*queue{"ops": {Waiting: []queued{{Event: event, Observer: "home", Queued: time.Now().Add(-48 * time.Hour), Tries: 4}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	endpoint := newEndpoint(t)
	n := &Notifier{Project: "lab", Instance: "primary", Path: path}
	if err = n.Load(); err != nil {
		t.Fatal(err)
	}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{{ID: "ops", Format: "json", URL: endpoint.srv.URL}}})
	defer n.Close()
	wait(t, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return len(n.queues["ops"].Waiting) == 0 })
	if hits := endpoint.hits.Load(); hits != 0 {
		t.Fatalf("48-hour-old alert was sent after restart (%d requests), despite 24-hour maxAge", hits)
	}
}

func TestAlertExpiresDuringBackoff(t *testing.T) {
	snap, changes := failing(t)
	endpoint := newEndpoint(t, 503)
	n := &Notifier{Project: "lab", Instance: "primary"}
	n.Backoff = func(int) time.Duration {
		n.mu.Lock()
		n.queues["ops"].Waiting[0].Queued = time.Now().Add(-48 * time.Hour)
		n.mu.Unlock()
		return 0
	}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{{ID: "ops", Format: "json", URL: endpoint.srv.URL}}})
	defer n.Close()
	n.Observe(snap, changes, false)
	wait(t, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return n.queues["ops"].Dropped == 1 })
	if hits := endpoint.hits.Load(); hits != 1 {
		t.Fatal("expired retry was sent", hits)
	}
}
