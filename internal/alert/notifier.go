package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Delivery bounds: what waits per webhook, how long an alert waits for a
// webhook that will not take it, the longest pause between tries, and the
// largest queue file a collector reads.
const (
	queueSize = 64
	maxAge    = 24 * time.Hour
	maxPause  = 5 * time.Minute
	maxBytes  = 16 << 20
)

// Notifier turns what a collector concludes into alerts and delivers them
// to the webhooks that take them, in order, one at a time per webhook, at
// least once; a full queue keeps each observer's share (see room). An
// alert a webhook fails to take, or takes with 408, 429 or a 5xx status,
// is tried again with pauses growing to five minutes until it has waited
// a day; one it refuses otherwise, or answers with a redirect, which is
// never followed, is dropped. With a Path, what waits is kept there, so a
// collector that stops or reloads loses none of it. Each drop is logged,
// by webhook ID: a webhook's URL is a secret and is never logged, nor
// kept with the alerts.
type Notifier struct {
	Project, Instance string
	Log               func(format string, args ...any)
	Client            *http.Client                    // nil: no proxy, no redirects, 10s timeout
	Backoff           func(attempt int) time.Duration // nil: 2^attempt seconds, at most five minutes
	MaxAge            time.Duration                   // how long an alert waits; zero: a day
	Path              string                          // where waiting alerts are kept; empty: in memory only

	configuring sync.Mutex // one Configure or Close at a time
	mu          sync.Mutex
	hooks       map[string]*hook
	queues      map[string]*queue // by webhook ID
	silent      map[string]bool   // observers told of as silent
	unwritten   bool              // the last write of Path failed
}

type hook struct {
	Webhook
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

// queue is what waits for a webhook, oldest first, and what came of the
// alerts it was sent.
type queue struct {
	Waiting []queued `json:"waiting"`
	Sent    int      `json:"sent"`
	Dropped int      `json:"dropped"`
}

type queued struct {
	Event    Event     `json:"event"`
	Observer string    `json:"observer"`
	Queued   time.Time `json:"queued"`
	Tries    int       `json:"tries,omitempty"`
}

type saved struct {
	Schema   int               `json:"schema"`
	Webhooks map[string]*queue `json:"webhooks"`
}

// Load reads what waited at Path when the collector stopped, to be sent
// once Configure names the webhooks again. A file it cannot read stops
// the collector rather than being replaced, as the incident ledger does.
func (n *Notifier) Load() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.queues = map[string]*queue{}
	if n.Path == "" {
		return nil
	}
	b, e := secure.ReadBounded(n.Path, maxBytes)
	switch {
	case errors.Is(e, os.ErrNotExist):
		return nil
	case e != nil:
		return e
	}
	var s saved
	if e = secure.Decode(strings.NewReader(string(b)), &s, maxBytes); e != nil || s.Schema != 1 {
		return fmt.Errorf("alert queue %s is invalid; move it aside to start a new one", n.Path)
	}
	for id, q := range s.Webhooks {
		if q == nil || len(q.Waiting) > queueSize {
			return fmt.Errorf("alert queue %s is invalid; move it aside to start a new one", n.Path)
		}
		n.queues[id] = q
		if len(q.Waiting) > 0 {
			n.logf("alerts: %d wait for webhook %s from before; %d sent and %d dropped so far", len(q.Waiting), id, q.Sent, q.Dropped)
		}
	}
	return nil
}

// Configure makes a's webhooks those alerts go to, as at start or on a
// reload. A webhook keeps what waits for it by its ID, sent as it is now
// configured; what waits for one removed is dropped.
func (n *Notifier) Configure(a Config) {
	n.configuring.Lock()
	defer n.configuring.Unlock()
	n.mu.Lock()
	next := map[string]*hook{}
	for _, w := range a.Webhooks {
		if h := n.hooks[w.ID]; h != nil && reflect.DeepEqual(h.Webhook, w) {
			next[w.ID] = h
		}
	}
	stopping := []*hook{}
	for id, h := range n.hooks {
		if next[id] != h {
			stopping = append(stopping, h)
		}
	}
	n.hooks = next
	n.mu.Unlock()
	// Stopped without the lock, which a delivery takes when it finishes.
	for _, h := range stopping {
		h.cancel()
		<-h.done
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.queues == nil {
		n.queues = map[string]*queue{}
	}
	named := map[string]bool{}
	for _, w := range a.Webhooks {
		named[w.ID] = true
		if n.queues[w.ID] == nil {
			n.queues[w.ID] = &queue{}
		}
		if n.hooks[w.ID] == nil {
			ctx, cancel := context.WithCancel(context.Background())
			h := &hook{Webhook: w, wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
			n.hooks[w.ID] = h
			go n.deliver(ctx, h)
		}
	}
	for id, q := range n.queues {
		if !named[id] {
			if len(q.Waiting) > 0 {
				n.logf("%d alerts for webhook %s dropped: it is no longer configured", len(q.Waiting), id)
			}
			delete(n.queues, id)
		}
	}
	n.save()
}

// Close stops delivering and waits until it has. What waits stays at Path
// for the next start.
func (n *Notifier) Close() {
	n.configuring.Lock()
	defer n.configuring.Unlock()
	n.mu.Lock()
	hooks := n.hooks
	n.hooks = nil
	n.mu.Unlock()
	for _, h := range hooks {
		h.cancel()
		<-h.done
	}
}

// Observe tells of what changed at snap, a snapshot of all observers: the
// incidents that opened and closed among changes and, once settled, the
// observers that fell silent or report again. An observer silent when
// the collector starts is told of once it has settled; one that reports
// again is told of only if it was told of as silent.
func (n *Notifier) Observe(snap model.Snapshot, changes []ledger.Change, settled bool) {
	v := view.View{Snapshot: snap, Now: snap.GeneratedAt}
	events := []Event{}
	for _, c := range changes {
		switch c.Kind {
		case "opened":
			events = append(events, incidentEvent(v, "incident.opened", c.Record, snap.GeneratedAt))
		case "closed":
			events = append(events, incidentEvent(v, "incident.closed", c.Record, snap.GeneratedAt))
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.silent == nil {
		n.silent = map[string]bool{}
	}
	known := map[string]bool{}
	for _, o := range snap.Observers {
		known[o.ID] = true
		switch up := o.Health == "up"; {
		case !up && !n.silent[o.ID] && settled:
			n.silent[o.ID] = true
			events = append(events, observerEvent(v, "observer.silent", o, snap.GeneratedAt))
		case up && n.silent[o.ID]:
			delete(n.silent, o.ID)
			events = append(events, observerEvent(v, "observer.back", o, snap.GeneratedAt))
		}
	}
	for id := range n.silent {
		if !known[id] {
			delete(n.silent, id)
		}
	}
	queuedAny := false
	for _, e := range events {
		for _, h := range n.hooks {
			if !h.wants(e.group(), e.Observer) {
				continue
			}
			q := n.queues[h.ID]
			if len(q.Waiting) >= queueSize {
				q.Dropped++
				gone, ok := q.room(e.Observer)
				if !ok {
					n.logf("alert %s from observer %s to webhook %s dropped: %d alerts already wait", e.Kind, e.Observer, h.ID, queueSize)
					continue
				}
				n.logf("alert %s from observer %s to webhook %s dropped for one from observer %s: %d alerts already wait, most of them from %s", gone.Event.Kind, gone.Observer, h.ID, e.Observer, queueSize, gone.Observer)
			}
			q.Waiting = append(q.Waiting, queued{Event: e, Observer: e.Observer, Queued: time.Now().UTC()})
			queuedAny = true
			select {
			case h.wake <- struct{}{}:
			default:
			}
		}
	}
	if queuedAny {
		n.save()
	}
}

// room makes room in a full q for an alert from observer. The newest
// alert of the observer with the most waiting gives way, if that is more
// than observer has, so that a worker, which can make as many incidents
// as it likes for its own observer, cannot crowd out another observer's
// alerts: a full queue holds each observer's fair share. The oldest stays
// whoever it is from, as it may be being sent.
func (q *queue) room(observer string) (queued, bool) {
	counts := map[string]int{}
	for _, w := range q.Waiting {
		counts[w.Observer]++
	}
	victim := 0
	for i := len(q.Waiting) - 1; i > 0; i-- {
		if victim == 0 || counts[q.Waiting[i].Observer] > counts[q.Waiting[victim].Observer] {
			victim = i
		}
	}
	if victim == 0 || counts[q.Waiting[victim].Observer] <= counts[observer] {
		return queued{}, false
	}
	gone := q.Waiting[victim]
	q.Waiting = slices.Delete(q.Waiting, victim, victim+1)
	return gone, true
}

// deliver sends what waits for h, oldest first, until ctx ends.
func (n *Notifier) deliver(ctx context.Context, h *hook) {
	defer close(h.done)
	for {
		n.mu.Lock()
		q := n.queues[h.ID]
		var next *queued
		if q != nil && len(q.Waiting) > 0 {
			v := q.Waiting[0]
			next = &v
		}
		n.mu.Unlock()
		if next == nil {
			select {
			case <-ctx.Done():
				return
			case <-h.wake:
				continue
			}
		}
		if !h.wants(next.Event.group(), next.Observer) {
			n.finish(h.ID, false, "alert %s to webhook %s dropped: it no longer takes it", next.Event.Kind, h.ID)
			continue
		}
		status, failure := n.post(ctx, h.Webhook, next.Event)
		switch {
		case ctx.Err() != nil:
			return
		case failure == "" && status/100 == 2:
			n.finish(h.ID, true, "")
			continue
		case failure == "" && (status/100 == 3 || status/100 == 4 && status != 408 && status != 429):
			n.finish(h.ID, false, "alert %s to webhook %s refused with HTTP %d; dropped", next.Event.Kind, h.ID, status)
			continue
		case failure == "":
			failure = "HTTP " + strconv.Itoa(status)
		}
		tries := n.tried(h.ID)
		if waited := time.Since(next.Queued); waited >= n.maxAge() {
			n.finish(h.ID, false, "alert %s to webhook %s dropped after waiting %s, tried %d times, last %s", next.Event.Kind, h.ID, waited.Round(time.Second), tries, failure)
			continue
		}
		pause := min(time.Duration(1)<<min(tries, 30)*time.Second, maxPause)
		if n.Backoff != nil {
			pause = n.Backoff(tries)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
	}
}

// finish takes the oldest alert waiting for webhook id off its queue, as
// sent or, with why, as dropped.
func (n *Notifier) finish(id string, sent bool, format string, args ...any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	q := n.queues[id]
	if q == nil || len(q.Waiting) == 0 {
		return
	}
	q.Waiting = q.Waiting[1:]
	if sent {
		q.Sent++
	} else {
		q.Dropped++
		n.logf(format, args...)
	}
	n.save()
}

// tried counts a failed try of the oldest alert waiting for webhook id.
func (n *Notifier) tried(id string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	q := n.queues[id]
	if q == nil || len(q.Waiting) == 0 {
		return 0
	}
	q.Waiting[0].Tries++
	n.save()
	return q.Waiting[0].Tries
}

func (n *Notifier) maxAge() time.Duration {
	if n.MaxAge > 0 {
		return n.MaxAge
	}
	return maxAge
}

// save writes what waits to Path. One it cannot write is kept in memory
// and said so, once, and again once it is written.
func (n *Notifier) save() {
	if n.Path == "" {
		return
	}
	b, e := json.Marshal(saved{Schema: 1, Webhooks: n.queues})
	if e == nil {
		e = secure.AtomicWrite(n.Path, b, 0600)
	}
	switch {
	case e != nil && !n.unwritten:
		n.logf("alert queue not written; keeping it in memory: %v", e)
	case e == nil && n.unwritten:
		n.logf("alert queue written again")
	}
	n.unwritten = e != nil
}

// post sends e to w once: the status it answered, or why it did not.
func (n *Notifier) post(ctx context.Context, w Webhook, e Event) (int, string) {
	req, err := w.request(ctx, e, n.Project, n.Instance)
	if err != nil {
		return 0, "invalid request"
	}
	client := n.Client
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, secure.ErrorCode(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode, ""
}

var defaultClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (n *Notifier) logf(format string, args ...any) {
	if n.Log != nil {
		n.Log(format, args...)
	}
}
