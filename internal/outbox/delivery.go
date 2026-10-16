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
	// Storage backpressure delays samples, not the executor's liveness.
	separateBeat bool
	// A collector that refuses a request carrying delivery telemetry may be
	// refusing the telemetry, not the samples: one older than it, or one
	// that finds it invalid, as when the clocks disagree. The request goes
	// again without it (probing) before any sample is blamed, and if that
	// is accepted, heartbeats go without it until bareUntil.
	probing              bool
	bareUntil            time.Time
	code                 string
	diagnostic           string
	lastAck, lastContact time.Time
	quarantined          int64
}

// bareFor is how long heartbeats go without delivery telemetry once a
// collector has refused it; it is offered again after, as the collector
// may have been upgraded.
const bareFor = time.Hour

type Delivery struct {
	destinations []destination
	Now          func() time.Time
	Project      string
	mu           sync.Mutex
	reports      map[string]model.DeliveryStatus
	revision     string // what the worker runs; a reload changes it mid-round
}
type Result struct {
	Code        string // stable diagnostic code for logs; retained during retry backoff
	Recipient   string
	Accepted    int
	Quarantined int
	Error       string
}

func NewDelivery(p client.Profile, revision string) (*Delivery, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	d := &Delivery{Now: time.Now, Project: p.Project, revision: revision}
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

// SetRevision says what topology the worker now runs, as after a reload.
func (d *Delivery) SetRevision(revision string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.revision = revision
}
func (d *Delivery) currentRevision() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.revision
}

// Tick is single-owner, and not to be run beside Run. It delivers one round
// to every collector at once and waits for them all.
func (d *Delivery) Tick(ctx context.Context, q *Queue, hb *model.Heartbeat) []Result {
	results := make([]Result, len(d.destinations))
	var wg sync.WaitGroup
	for i := range d.destinations {
		wg.Go(func() { results[i] = d.send(ctx, q, i, hb) })
	}
	wg.Wait()
	return results
}

// Run delivers to every collector from a loop of its own, a round each
// interval once any backoff has passed, until ctx ends. A collector slow
// to answer or not answering holds up only its own next round; the others
// keep receiving on time. beat gives the heartbeat for a round, and report
// is handed each round's result, one at a time.
func (d *Delivery) Run(ctx context.Context, q *Queue, interval time.Duration, beat func() *model.Heartbeat, report func(Result)) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range d.destinations {
		wg.Go(func() {
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
				r := d.send(ctx, q, i, beat())
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				report(r)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
}

// send delivers one round to destination i: what is pending for it, with
// hb when it describes the topology the batch was queued under. Only the
// goroutine delivering to i touches its retry state.
func (d *Delivery) send(ctx context.Context, q *Queue, i int, hb *model.Heartbeat) (result Result) {
	dest := &d.destinations[i]
	rid := Recipient(dest.endpoint.URL)
	result = Result{Recipient: rid}
	defer func() {
		result.Code = dest.diagnostic
		if result.Code == "" || dest.code == "local_ack_failed" || dest.code == "local_quarantine_failed" {
			result.Code = dest.code
		}
		d.publish(q, dest, result)
	}()
	revision := d.currentRevision()
	now := d.Now()
	if now.Before(dest.next) {
		if dest.separateBeat && hb != nil {
			if e := d.heartbeat(ctx, dest, revision, hb); e != nil {
				dest.separateBeat = false // an outage follows the normal retry clock
				result.Error = "collector heartbeat unavailable"
			}
		}
		return
	}
	itemLimit := 100
	if dest.isolating {
		itemLimit = 1
	}
	items := q.Pending(rid, itemLimit)
	if len(items) == 0 {
		dest.isolating = false
		dest.separateBeat = false
	}
	batch := model.Batch{Schema: model.BatchSchema, Project: d.Project, Revision: revision, Observations: []model.Observation{}}
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
	if hb != nil && batch.Revision == revision && batch.Schema == model.BatchSchema {
		h := d.beat(dest, hb, now)
		batch.Heartbeat = &h
	}
	if len(batch.Observations) == 0 && batch.Heartbeat == nil {
		return
	}
	telemetry := batch.Heartbeat != nil && len(batch.Heartbeat.Delivery) > 0
	probed := dest.probing
	e := client.PostBatch(ctx, dest.http, dest.endpoint, batch)
	if e == nil {
		dest.lastContact = d.Now()
		dest.code = ""
		dest.diagnostic = ""
	} else {
		dest.code = deliveryCode(e)
		dest.diagnostic = diagnosticCode(e)
	}
	var he *client.HTTPError
	permanent := errors.As(e, &he) && he.Permanent()
	switch {
	case telemetry && permanent:
		// Not yet the samples' fault: they go again at once, bare.
		dest.probing = true
		dest.next = time.Time{}
		result.Error = he.Error() + "; resending without delivery telemetry"
		return
	case probed && e == nil:
		// The telemetry was what the collector refused.
		dest.probing = false
		dest.bareUntil = d.Now().Add(bareFor)
	case probed && permanent:
		// Refused bare too, so the samples are; the telemetry was not.
		dest.probing = false
	}
	if e == nil {
		if e = q.Ack(ids, rid); e != nil {
			dest.code = "local_ack_failed"
			result.Error = "local acknowledgement persistence failed"
			return
		}
		// Accepted: whatever was refused before has been dealt with, so
		// items travel together again.
		dest.failures = 0
		dest.next = time.Time{}
		dest.isolating = false
		dest.separateBeat = false
		result.Accepted = len(batch.Observations)
		return
	}
	dest.separateBeat = errors.As(e, &he) && (he.Status == 429 && he.Code == "observer_storage_quota" || he.Status == 503 && he.Code == "storage_receipt_capacity")
	if dest.separateBeat && hb != nil {
		if d.heartbeat(ctx, dest, revision, hb) != nil {
			dest.separateBeat = false
		}
	}
	if permanent {
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
			dest.code = "local_quarantine_failed"
			result.Error = "quarantine persistence failed"
		} else {
			// The collector answered and the refused item is set aside:
			// that is progress, not an outage, so the next item goes at
			// the next tick rather than after a backoff (a reload can
			// leave many samples of an old contract).
			result.Quarantined = len(batch.Observations)
			result.Error = he.Error()
			dest.failures = 0
			dest.next = time.Time{}
			return
		}
	} else {
		result.Error = "collector unavailable or acknowledgement invalid"
		if he != nil {
			result.Error = he.Error()
		}
	}
	dest.failures++
	n := dest.failures
	if n > 5 {
		n = 5
	}
	// The backoff counts from when the attempt ended, which for one that
	// timed out is long after it began.
	delay := time.Duration(1<<n) * time.Second
	dest.next = d.Now().Add(delay + time.Duration(rand.IntN(1000))*time.Millisecond)
	return
}

// heartbeat consumes no pending item and never acknowledges observations.
// It always describes the current worker, even while old samples wait.
func (d *Delivery) heartbeat(ctx context.Context, dest *destination, revision string, hb *model.Heartbeat) error {
	h := d.beat(dest, hb, d.Now())
	err := client.PostBatch(ctx, dest.http, dest.endpoint, model.Batch{Schema: model.BatchSchema, Project: d.Project, Revision: revision, Heartbeat: &h})
	if err == nil {
		dest.lastContact = d.Now()
	}
	return err
}

// beat is the heartbeat to send dest: hb with the delivery telemetry,
// unless dest has refused it lately or is being asked whether it did.
func (d *Delivery) beat(dest *destination, hb *model.Heartbeat, now time.Time) model.Heartbeat {
	h := model.CloneHeartbeat(*hb)
	if !dest.probing && !now.Before(dest.bareUntil) {
		h.Delivery = d.Statuses()
	}
	return h
}
