package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type destination struct {
	// endpoint and http are immutable after construction. Read them by index
	// or pointer: copying the whole destination also reads Tick's retry state.
	endpoint  client.Endpoint
	http      *http.Client
	next      time.Time
	failures  int
	isolating bool
}
type Delivery struct {
	destinations      []destination
	Now               func() time.Time
	Project, Revision string
}
type Result struct {
	Recipient   string
	Accepted    int
	Quarantined int
	Error       string
}

func NewDelivery(p client.Profile, revision string) (*Delivery, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	d := &Delivery{Now: time.Now, Project: p.Project, Revision: revision}
	for _, ep := range p.Servers {
		h, e := client.HTTP(ep)
		if e != nil {
			d.Close()
			return nil, e
		}
		d.destinations = append(d.destinations, destination{endpoint: ep, http: h})
	}
	return d, nil
}
func (d *Delivery) Recipients() []string {
	r := []string{}
	for i := range d.destinations {
		r = append(r, Recipient(d.destinations[i].endpoint.URL))
	}
	return r
}
func (d *Delivery) Close() {
	for i := range d.destinations {
		d.destinations[i].http.CloseIdleConnections()
	}
}

// Tick is single-owner. It delivers to independent collectors concurrently so
// an unavailable destination cannot prevent the other destination receiving.
func (d *Delivery) Tick(ctx context.Context, q *Queue, hb *model.Heartbeat) []Result {
	results := make([]Result, len(d.destinations))
	var wg sync.WaitGroup
	for i := range d.destinations {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dest := &d.destinations[i]
			rid := Recipient(dest.endpoint.URL)
			result := Result{Recipient: rid}
			defer func() { results[i] = result }()
			now := d.Now()
			if now.Before(dest.next) {
				return
			}
			itemLimit := 100
			if dest.isolating {
				itemLimit = 1
			}
			items := q.Pending(rid, itemLimit)
			if len(items) == 0 {
				dest.isolating = false
			}
			batch := model.Batch{Schema: model.BatchSchema, Project: d.Project, Revision: d.Revision, Observations: []model.Observation{}}
			ids := []string{}
			if len(items) > 0 {
				// Items queued before an upgrade or a reload travel with
				// their own schema and revision, never mixed with others.
				batch.Schema = items[0].Batch.Schema
				batch.Project = items[0].Batch.Project
				batch.Revision = items[0].Batch.Revision
				for _, v := range items {
					if v.Batch.Schema != batch.Schema || v.Batch.Project != batch.Project || v.Batch.Revision != batch.Revision || len(batch.Observations)+len(v.Batch.Observations) > 100 {
						continue
					}
					previous := len(batch.Observations)
					batch.Observations = append(batch.Observations, v.Batch.Observations...)
					wire, _ := json.Marshal(batch)
					// Leave room for the optional heartbeat and HTTP decoder budget.
					if len(wire) > (1<<20)-4096 {
						batch.Observations = batch.Observations[:previous]
						continue
					}
					ids = append(ids, v.ID)
				}
			}
			if hb != nil && batch.Revision == d.Revision && batch.Schema == model.BatchSchema {
				h := *hb
				batch.Heartbeat = &h
			}
			if len(batch.Observations) == 0 && batch.Heartbeat == nil {
				return
			}
			e := client.PostBatch(ctx, dest.http, dest.endpoint, batch)
			if e == nil {
				if e = q.Ack(ids, rid); e != nil {
					result.Error = "local acknowledgement persistence failed"
					return
				}
				dest.failures = 0
				dest.next = time.Time{}
				result.Accepted = len(batch.Observations)
				return
			}
			var he *client.HTTPError
			if errors.As(e, &he) && he.Permanent() {
				if len(ids) > 1 {
					// A single expired or invalid item rejects the entire request.
					// Retry original durable items separately before quarantining
					// anything, so valid neighbouring samples are still delivered.
					dest.isolating = true
					dest.next = time.Time{}
					result.Error = "combined batch rejected; retrying individual outbox items"
					return
				}
				if qe := q.Quarantine(ids, rid, he.Error()); qe != nil {
					result.Error = "quarantine persistence failed"
				} else {
					result.Quarantined = len(batch.Observations)
					result.Error = he.Error()
				}
			} else {
				result.Error = "collector unavailable or acknowledgement invalid"
			}
			dest.failures++
			n := dest.failures
			if n > 5 {
				n = 5
			}
			delay := time.Duration(1<<n) * time.Second
			dest.next = now.Add(delay + time.Duration(rand.IntN(1000))*time.Millisecond)
		}(i)
	}
	wg.Wait()
	return results
}
