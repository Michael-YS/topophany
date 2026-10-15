package outbox

import (
	"context"
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
				clock.Add(int64(500 * time.Millisecond)) // still within sample backoff
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
