package outbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestRejectedAggregateDoesNotQuarantineValidNeighbour(t *testing.T) {
	var accepted atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b model.Batch
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Error(e)
			return
		}
		for _, v := range b.Observations {
			if v.Sequence == 1 {
				w.WriteHeader(422)
				json.NewEncoder(w).Encode(map[string]string{"error": "sample_time_out_of_range"})
				return
			}
		}
		accepted.Add(int64(len(b.Observations)))
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{"project": b.Project, "revision": b.Revision, "accepted": len(b.Observations)})
	}))
	defer s.Close()
	q := queue(t)
	rid := Recipient(s.URL)
	for seq := uint64(1); seq <= 2; seq++ {
		if e := q.Add(makeBatch(seq), []string{rid}); e != nil {
			t.Fatal(e)
		}
	}
	d, e := NewDelivery(client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: s.URL, Token: workerToken}}}, makeBatch(1).Revision)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	now := time.Now()
	d.Now = func() time.Time { return now }
	first := d.Tick(context.Background(), q, nil)
	if first[0].Quarantined != 0 || len(q.Pending(rid, 10)) != 2 {
		t.Fatal("aggregate rejection discarded neighbours", first)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(time.Minute)
		d.Tick(context.Background(), q, nil)
	}
	if accepted.Load() != 1 || len(q.Pending(rid, 10)) != 0 {
		t.Fatal("valid neighbour was not delivered", accepted.Load())
	}
}
