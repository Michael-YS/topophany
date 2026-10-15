package api

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/model"
	"net/http"
	"testing"
)

func TestDeliveryStatusScopedBoundedAndDetached(t *testing.T) {
	srv, h, b := fixture(t)
	now := srv.Now()
	b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "worker", SampledAt: now, Delivery: []model.DeliveryStatus{{Collector: "0123456789abcdef", ReportedAt: now, PendingRecords: 2, PendingBytes: 2048, Code: "storage_backpressure"}}}
	raw, _ := json.Marshal(b)
	w := request(h, "POST", "/v1/observations", workerToken, "application/json", raw)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body)
	}
	w = request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	var snap model.Snapshot
	if e := json.Unmarshal(w.Body.Bytes(), &snap); e != nil {
		t.Fatal(e)
	}
	var found bool
	for _, o := range snap.Observers {
		if o.ID == "home" {
			found = len(o.Delivery) == 1 && o.Delivery[0].PendingRecords == 2
		}
	}
	if !found {
		t.Fatal("delivery omitted", snap.Observers)
	}
	_, beats := srv.Store.View()
	x := beats["home"]
	x.Delivery[0].Code = "mutated"
	_, beats = srv.Store.View()
	if beats["home"].Delivery[0].Code != "storage_backpressure" {
		t.Fatal("mutable store heartbeat")
	}
	b.Heartbeat.Delivery[0].Code = "secret-from-peer"
	raw, _ = json.Marshal(b)
	w = request(h, "POST", "/v1/observations", workerToken, "application/json", raw)
	if w.Code != 400 {
		t.Fatal("arbitrary status reflected", w.Code)
	}
}
