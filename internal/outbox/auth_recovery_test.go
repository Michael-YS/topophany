package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorizationOutageRetainsPendingSamples(t *testing.T) {
	for _, status := range []int{401, 403} {
		for _, isolated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/isolation=%v", status, isolated), func(t *testing.T) {
				var authorized atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !authorized.Load() {
						w.WriteHeader(status)
						fmt.Fprint(w, `{"error":"invalid_token"}`)
						return
					}
					var b model.Batch
					json.NewDecoder(r.Body).Decode(&b)
					w.WriteHeader(202)
					json.NewEncoder(w).Encode(map[string]any{"project": b.Project, "revision": b.Revision, "accepted": len(b.Observations)})
				}))
				defer server.Close()
				b := makeBatch(1)
				if isolated {
					b.Observations = append(b.Observations, makeBatch(2).Observations[0])
				}
				profile := client.Profile{Project: b.Project, Servers: []client.Endpoint{{URL: server.URL, Token: workerToken}}}
				d, e := NewDelivery(profile, b.Revision)
				if e != nil {
					t.Fatal(e)
				}
				defer d.Close()
				q := queue(t)
				if e = q.Add(b, d.Recipients()); e != nil {
					t.Fatal(e)
				}
				now := time.Now()
				d.Now = func() time.Time { return now }
				d.destinations[0].isolating = isolated
				for range 3 {
					r := d.Tick(context.Background(), q, nil)
					if r[0].Quarantined != 0 {
						t.Fatal(r)
					}
					now = now.Add(time.Minute)
				}
				if n, _ := q.Stats(); n != 1 {
					t.Fatalf("pending=%d", n)
				}
				files, e := os.ReadDir(filepath.Join(q.dir, "quarantine"))
				if e != nil || len(files) != 0 {
					t.Fatal(files, e)
				}
				// Credential repair may restart the worker; durable pending data survives.
				d.Close()
				q.Close()
				q, e = Open(q.dir, b.Project, 1<<20)
				if e != nil {
					t.Fatal(e)
				}
				defer q.Close()
				d, e = NewDelivery(profile, b.Revision)
				if e != nil {
					t.Fatal(e)
				}
				defer d.Close()
				authorized.Store(true)
				if r := d.Tick(context.Background(), q, nil); r[0].Accepted != len(b.Observations) {
					t.Fatal(r)
				}
				if n, _ := q.Stats(); n != 0 {
					t.Fatal(n)
				}
			})
		}
	}
}
