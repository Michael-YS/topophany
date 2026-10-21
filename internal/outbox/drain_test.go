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

func TestBacklogDrainsWithinRequestBudgetAndStopsOn429(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget", true: "429"}[limited], func(t *testing.T) {
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var b model.Batch
				if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
					t.Error(err)
					return
				}
				if limited && n == 2 {
					w.WriteHeader(429)
					json.NewEncoder(w).Encode(map[string]string{"error": "rate_limited"})
					return
				}
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(map[string]any{"project": b.Project, "revision": b.Revision, "accepted": len(b.Observations)})
			}))
			defer srv.Close()
			d, err := NewDelivery(client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, makeBatch(1).Revision)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			q := queue(t)
			for seq := uint64(1); seq <= 450; seq++ {
				if err := q.Add(makeBatch(seq), d.Recipients()); err != nil {
					t.Fatal(err)
				}
			}
			accepted := 0
			begin := time.Now()
			d.drain(context.Background(), q, 0, func() *model.Heartbeat { return nil }, func(r Result) { accepted += r.Accepted })
			wantCalls, wantAccepted := int64(4), 400
			if limited {
				wantCalls, wantAccepted = 2, 100
			}
			if calls.Load() != wantCalls || accepted != wantAccepted {
				t.Fatal(calls.Load(), accepted)
			}
			if time.Since(begin) < time.Duration(wantCalls-1)*100*time.Millisecond {
				t.Fatal("burst was not paced")
			}
			if limited {
				if d.destinations[0].next.IsZero() {
					t.Fatal("429 did not retain backoff")
				}
				d.drain(context.Background(), q, 0, func() *model.Heartbeat { return nil }, func(Result) {})
				if calls.Load() != wantCalls {
					t.Fatal("burst bypassed backoff")
				}
			}
		})
	}
}
