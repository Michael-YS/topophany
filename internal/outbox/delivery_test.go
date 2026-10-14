package outbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestDeliveryConcurrentMetadataAccess(t *testing.T) {
	for _, method := range []string{"Recipients", "Close"} {
		t.Run(method, func(t *testing.T) {
			c := testkit.Config()
			var up atomic.Bool
			srv, _ := collector(t, c, &up)
			d, err := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, config.Revision(c))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			q := queue(t)
			want := []string{Recipient(srv.URL)}
			if err := q.Add(makeBatch(1), want); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			d.Now = func() time.Time { return now }

			started, stop, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				close(started)
				for {
					select {
					case <-stop:
						return
					default:
					}
					if method == "Recipients" {
						if got := d.Recipients(); !slices.Equal(got, want) {
							t.Errorf("recipients = %v, want %v", got, want)
							return
						}
					} else {
						d.Close()
					}
					runtime.Gosched()
				}
			}()
			defer func() { close(stop); <-done }()
			<-started

			// Exercise retry-state writes while the other goroutine reads only
			// immutable metadata, then verify recovery still acknowledges data.
			for range 20 {
				results := d.Tick(context.Background(), q, nil)
				if results[0].Error == "" {
					t.Fatal("unavailable collector did not fail", results)
				}
				now = now.Add(time.Minute)
			}
			up.Store(true)
			if results := d.Tick(context.Background(), q, nil); results[0].Accepted != 1 || results[0].Error != "" {
				t.Fatal("collector did not recover", results)
			}
			if n, _ := q.Stats(); n != 0 {
				t.Fatal("acknowledged data still pending", n)
			}
		})
	}
}

// A collector that takes its time answering holds up only its own next
// round: the other receives each batch as it is queued.
func TestASlowCollectorDelaysNoOther(t *testing.T) {
	c := testkit.Config()
	healthy, s := collector(t, c, nil)
	arrived, release := make(chan struct{}, 8), make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "too late", 503)
	}))
	defer slow.Close()
	defer close(release)
	d, e := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: healthy.URL, Token: workerToken}, {URL: slow.URL, Token: workerToken}}}, config.Revision(c))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	q := queue(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx, q, 50*time.Millisecond, func() *model.Heartbeat { return nil }, func(Result) {})
	}()
	defer func() { cancel(); <-done }()
	for seq := uint64(1); seq <= 3; seq++ {
		if e := q.Add(makeBatch(seq), d.Recipients()); e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			if rows, _ := s.View(); len(rows) == int(seq) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("batch %d did not reach the healthy collector while the other was answering", seq)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if seq == 1 {
			<-arrived
		}
	}
	if len(arrived) != 0 {
		t.Fatal("the slow collector was sent another round before it answered the first")
	}
}
