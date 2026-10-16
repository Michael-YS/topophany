package outbox

import (
	"context"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeliveryReportsBackpressureAndDataACKSeparately(t *testing.T) {
	blocked := true
	var received model.Batch
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		if blocked && len(received.Observations) > 0 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"observer_storage_quota"}`))
			return
		}
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{"project": received.Project, "revision": received.Revision, "accepted": len(received.Observations)})
	}))
	defer server.Close()
	c := testkit.Config()
	clock := time.Now().UTC()
	now := &clock
	d, err := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: workerToken}}}, config.Revision(c))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Now = func() time.Time { return *now }
	q, err := Open(t.TempDir(), c.Project, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	batch := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, *now, "ok")}}
	if err = q.Add(batch, d.Recipients()); err != nil {
		t.Fatal(err)
	}
	hb := &model.Heartbeat{Observer: "home", Boot: "test", SampledAt: *now}
	d.Tick(context.Background(), q, hb)
	status := d.Statuses()[0]
	if status.Code != "storage_backpressure" || status.PendingRecords != 1 || !status.LastAck.IsZero() || status.LastContact.IsZero() {
		t.Fatal(status)
	}
	*now = now.Add(time.Minute)
	blocked = false
	d.Tick(context.Background(), q, hb)
	status = d.Statuses()[0]
	if status.Code != "" || status.PendingRecords != 0 || status.LastAck.IsZero() {
		t.Fatal(status)
	}
	*now = now.Add(time.Second)
	d.Tick(context.Background(), q, hb)
	if len(received.Heartbeat.Delivery) != 1 {
		t.Fatal(received)
	}
	reports := d.Statuses()
	reports[0].Code = "mutated"
	if d.Statuses()[0].Code != "" {
		t.Fatal("reports share memory")
	}
}

func TestDeliveryDiagnosticsPreserveContractMismatch(t *testing.T) {
	if got := diagnosticCode(&client.HTTPError{Status: 409, Code: "contract_mismatch"}); got != "contract_mismatch" {
		t.Fatal(got)
	}
	if got := diagnosticCode(&client.HTTPError{Status: 409, Code: "fixture-private-response"}); got != "sample_rejected" {
		t.Fatal("arbitrary upstream response reached diagnostic", got)
	}
}
