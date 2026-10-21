package outbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestStorageBackpressureDoesNotStopHeartbeats(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(map[bool]string{false: "global", true: "observer"}[scoped], func(t *testing.T) {
			c := testkit.Config()
			var clock atomic.Int64
			clock.Store(time.Now().UnixNano())
			now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
			opts := store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c), MaxReceipts: 1, Retention: time.Minute, Now: now}
			st, e := store.Open(t.TempDir(), opts)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			backend := api.New(c, "test", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(workerToken), Role: "worker", Observer: "home"}}})
			backend.Now = now
			if scoped {
				st.SetObservers([]string{"home"})
			} else {
				st.SetObservers(nil)
			}
			seed := makeBatch(1)
			seed.Observations[0].SampledAt = now().Add(-2 * time.Minute)
			if e = st.Put(seed); e != nil {
				t.Fatal(e)
			}
			srv := httptest.NewServer(backend.Handler())
			defer srv.Close()
			d, e := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, config.Revision(c))
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close()
			d.Now = now
			q := queue(t)
			if e = q.Add(makeBatch(2), d.Recipients()); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 3; i++ {
				beat := &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now()}
				r := d.Tick(context.Background(), q, beat)
				_, beats := st.View()
				if !beats["home"].SampledAt.Equal(beat.SampledAt) {
					t.Fatal("heartbeat not delivered", r, beats)
				}
				if n, _ := q.Stats(); n != 1 || r[0].Accepted != 0 {
					t.Fatal("heartbeat acknowledged pending sample", n, r)
				}
				clock.Add(int64(time.Second)) // respect the collector's heartbeat durability cadence
			}
			clock.Add(int64(2 * time.Hour)) // receipt expires; queued sample still within API ingestion window
			if r := d.Tick(context.Background(), q, &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now()}); r[0].Accepted != 1 || r[0].Error != "" {
				t.Fatal("delivery did not resume", r)
			}
			if n, _ := q.Stats(); n != 0 {
				t.Fatal("accepted sample still queued")
			}
		})
	}
}

func TestIdleCadenceKeepsSamplesPromptAndLivenessIndependent(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var b model.Batch
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"project": b.Project, "revision": b.Revision, "accepted": len(b.Observations)})
	}))
	defer srv.Close()
	d, err := NewDelivery(client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, makeBatch(1).Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.SetHeartbeatInterval(15 * time.Second)
	q := queue(t)
	now := time.Now().UTC()
	d.Now = func() time.Time { return now }
	hb := &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now}
	round := func(want int64) {
		t.Helper()
		hb.SampledAt = now
		if got := d.Tick(context.Background(), q, hb); got[0].Error != "" {
			t.Fatal(got)
		}
		if got := calls.Load(); got != want {
			t.Fatalf("requests=%d want %d", got, want)
		}
	}
	round(1)
	for range 7 {
		now = now.Add(2 * time.Second)
		round(1)
	}
	if err := q.Add(makeBatch(1), d.Recipients()); err != nil {
		t.Fatal(err)
	}
	round(2) // A sample never waits for the idle heartbeat deadline.
	now = now.Add(14 * time.Second)
	round(2) // Piggybacked liveness restarts the idle deadline.
	now = now.Add(time.Second)
	round(3)
	hb.Boot = "restarted"
	round(4)
	hb.Contracts = "changed"
	round(5)
	d.SetRevision("new revision")
	round(6)
	now = now.Add(-time.Minute)
	round(7) // A clock rollback must not silence liveness until it catches up.
}
