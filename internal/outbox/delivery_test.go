package outbox

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"runtime"
	"slices"
	"strings"
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
					d.Statuses()
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

// A collector that refuses a request carrying delivery telemetry, as one
// older than the worker does (it knows no such field), costs no sample:
// the request goes again at once without it, heartbeats go without it for
// a while, then offer it again, and an upgraded collector receives it.
// Samples the collector refuses bare are still set aside, one at a time.
func TestRefusedTelemetryIsNotBlamedOnSamples(t *testing.T) {
	c := testkit.Config()
	inner, s := collector(t, c, nil)
	target, _ := url.Parse(inner.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var old atomic.Bool // the collector predates delivery telemetry
	old.Store(true)
	var bad atomic.Value // the ID of an observation the collector refuses
	bad.Store("")
	var telemetry atomic.Int64
	refuse := func(w http.ResponseWriter, code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"`+code+`"}`)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"delivery"`)) {
			telemetry.Add(1)
			if old.Load() {
				refuse(w, "invalid_batch_json")
				return
			}
		}
		if id := bad.Load().(string); id != "" && bytes.Contains(body, []byte(id)) {
			refuse(w, "sample_too_old")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		proxy.ServeHTTP(w, r)
	}))
	defer srv.Close()
	d, e := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, config.Revision(c))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	// The worker's clock runs behind, so its reports stay valid as it moves.
	now := time.Now().Add(-3 * time.Hour)
	d.Now = func() time.Time { return now }
	q := queue(t)
	seq := uint64(0)
	round := func(add bool) Result {
		t.Helper()
		if add {
			seq++
			if e := q.Add(makeBatch(seq), d.Recipients()); e != nil {
				t.Fatal(e)
			}
		}
		return d.Tick(context.Background(), q, &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: time.Now()})[0]
	}
	want := func(r Result, accepted, quarantined int, err string, sent int64) {
		t.Helper()
		if r.Accepted != accepted || r.Quarantined != quarantined || !strings.Contains(r.Error, err) || err == "" && r.Error != "" || telemetry.Load() != sent {
			t.Fatalf("got %+v with telemetry sent %d times; want %d accepted, %d quarantined, error %q, telemetry sent %d times", r, telemetry.Load(), accepted, quarantined, err, sent)
		}
	}

	want(round(true), 1, 0, "", 0) // nothing to report yet
	want(round(true), 0, 0, "invalid_batch_json; resending without delivery telemetry", 1)
	want(round(false), 1, 0, "", 1)
	want(round(true), 1, 0, "", 1) // still bare
	now = now.Add(bareFor + time.Second)
	want(round(true), 0, 0, "resending without", 2) // offered again, refused again
	want(round(false), 1, 0, "", 2)

	old.Store(false) // upgraded
	now = now.Add(bareFor + time.Second)
	want(round(true), 1, 0, "", 3)
	if _, beats := s.View(); len(beats["home"].Delivery) != 1 {
		t.Fatal("the upgraded collector holds no delivery telemetry", beats["home"])
	}

	bad.Store(makeBatch(seq + 1).Observations[0].ID)
	want(round(true), 0, 0, "sample_too_old; resending without", 4)
	want(round(false), 0, 1, "sample_too_old", 4) // refused bare: set aside
	want(round(true), 1, 0, "", 5)                // and the telemetry is not withheld
	if n, _ := q.Stats(); n != 0 {
		t.Fatal("samples still pending", n)
	}
}
