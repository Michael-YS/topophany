package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
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

func TestDeliveryRejectionBranches(t *testing.T) {
	now := time.Now().UTC()
	good := model.DeliveryStatus{Collector: "0123456789abcdef", ReportedAt: now, PendingRecords: 1, PendingBytes: 1}
	cases := []struct {
		name   string
		mutate func(*model.DeliveryStatus, *[]model.DeliveryStatus)
	}{
		{"too many", func(_ *model.DeliveryStatus, all *[]model.DeliveryStatus) {
			*all = make([]model.DeliveryStatus, 9)
			for i := range *all {
				(*all)[i] = good
				(*all)[i].Collector = "0123456789abcd" + string(rune('a'+i))
			}
		}},
		{"short collector", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.Collector = "abcd" }},
		{"non hex", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.Collector = "0123456789abcdeg" }},
		{"duplicate", func(r *model.DeliveryStatus, all *[]model.DeliveryStatus) {
			*all = []model.DeliveryStatus{*r, *r}
		}},
		{"negative records", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.PendingRecords = -1 }},
		{"negative bytes", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.PendingBytes = -1 }},
		{"negative quarantine", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.QuarantineBytes = -1 }},
		{"negative quarantined samples", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.QuarantinedSamples = -1 }},
		{"zero reported", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.ReportedAt = time.Time{} }},
		{"reported ahead", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.ReportedAt = now.Add(time.Minute) }},
		{"ack ahead", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.LastAck = now.Add(time.Minute) }},
		{"contact ahead", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.LastContact = now.Add(time.Minute) }},
		{"retry ahead", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.RetryAt = now.Add(2 * time.Minute) }},
		{"unknown code", func(r *model.DeliveryStatus, _ *[]model.DeliveryStatus) { r.Code = "nope" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, h, b := fixture(t)
			srv.Now = func() time.Time { return now }
			report := good
			reports := []model.DeliveryStatus{report}
			tc.mutate(&report, &reports)
			if len(reports) == 1 {
				reports[0] = report
			}
			b.Observations = nil
			b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now, Delivery: reports}
			w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
			if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_delivery_status") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	t.Run("within skew", func(t *testing.T) {
		srv, h, b := fixture(t)
		srv.Now = func() time.Time { return now }
		report := good
		report.ReportedAt = now.Add(30 * time.Second)
		report.LastAck = now.Add(30 * time.Second)
		report.RetryAt = now.Add(time.Minute)
		report.Code = "sample_rejected"
		b.Observations = nil
		b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now, Delivery: []model.DeliveryStatus{report}}
		if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		_ = srv
	})
	t.Run("collector step back", func(t *testing.T) {
		srv, h, b := fixture(t)
		start := now
		srv.Now = func() time.Time { return start }
		b.Observations[0].SampledAt = start
		if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		srv.Now = func() time.Time { return start.Add(-time.Minute) }
		report := good
		report.ReportedAt = start.Add(2 * time.Second)
		b.Observations = nil
		b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: start.Add(2 * time.Second), Delivery: []model.DeliveryStatus{report}}
		if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
	})
}

func TestWorkerEnrollmentCheckIsReadOnlyAndScoped(t *testing.T) {
	_, h, _ := fixture(t)
	w := request(h, "GET", "/v1/worker", workerToken, "", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var info map[string]string
	json.Unmarshal(w.Body.Bytes(), &info)
	if info["observer"] != "home" || info["project"] == "" {
		t.Fatal(info)
	}
	if w = request(h, "GET", "/v1/worker", viewerToken, "", nil); w.Code != 403 {
		t.Fatal("viewer acquired worker identity", w.Code)
	}
}
