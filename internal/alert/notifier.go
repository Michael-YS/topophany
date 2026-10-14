package alert

import (
	"context"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// Delivery bounds: what waits per webhook, and how often an alert is
// tried before it is dropped.
const (
	queueSize = 64
	attempts  = 6
)

// Notifier turns what a collector concludes into alerts and delivers them
// to the webhooks that take them, in order, one at a time per webhook. An
// alert a webhook fails to take, or takes with 408, 429 or a 5xx status,
// is tried again with growing pauses, six times in all; one it refuses
// otherwise, or answers with a redirect, which is never followed, is
// dropped. Either is logged, by webhook ID: a webhook's URL is a secret
// and never logged.
type Notifier struct {
	Project, Instance string
	Log               func(format string, args ...any)
	Client            *http.Client                    // nil: no proxy, no redirects, 10s timeout
	Backoff           func(attempt int) time.Duration // nil: 2^attempt seconds

	mu     sync.Mutex
	hooks  map[string]*hook
	silent map[string]bool // observers told of as silent
}

type hook struct {
	Webhook
	queue  chan Event
	cancel context.CancelFunc
	done   chan struct{}
}

// Configure makes a's webhooks those alerts go to, as at start or on a
// reload. A webhook unchanged keeps what it has queued; one changed or
// removed drops it.
func (n *Notifier) Configure(a Config) {
	n.mu.Lock()
	defer n.mu.Unlock()
	next := map[string]*hook{}
	for _, w := range a.Webhooks {
		if h := n.hooks[w.ID]; h != nil && reflect.DeepEqual(h.Webhook, w) {
			next[w.ID] = h
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		h := &hook{Webhook: w, queue: make(chan Event, queueSize), cancel: cancel, done: make(chan struct{})}
		go n.deliver(ctx, h)
		next[w.ID] = h
	}
	for id, h := range n.hooks {
		if next[id] != h {
			h.cancel()
			<-h.done
		}
	}
	n.hooks = next
}

// Close stops delivering and waits until it has; what is queued is lost.
func (n *Notifier) Close() { n.Configure(Config{}) }

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
	for _, e := range events {
		for _, h := range n.hooks {
			if !h.wants(e.group(), e.Observer) {
				continue
			}
			select {
			case h.queue <- e:
			default:
				n.logf("alert %s to webhook %s dropped: %d alerts already wait", e.Kind, h.ID, queueSize)
			}
		}
	}
	n.mu.Unlock()
}

func (n *Notifier) deliver(ctx context.Context, h *hook) {
	defer close(h.done)
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-h.queue:
			n.send(ctx, h.Webhook, e)
		}
	}
}

// send tries e at w until it is taken, refused or tried too often.
func (n *Notifier) send(ctx context.Context, w Webhook, e Event) {
	for attempt := 1; ; attempt++ {
		status, failure := n.post(ctx, w, e)
		switch {
		case ctx.Err() != nil, failure == "" && status/100 == 2:
			return
		case failure == "" && (status/100 == 3 || status/100 == 4 && status != 408 && status != 429):
			n.logf("alert %s to webhook %s refused with HTTP %d; dropped", e.Kind, w.ID, status)
			return
		case failure == "":
			failure = "HTTP " + strconv.Itoa(status)
		}
		if attempt == attempts {
			n.logf("alert %s to webhook %s failed %d times, last %s; dropped", e.Kind, w.ID, attempts, failure)
			return
		}
		pause := time.Duration(1<<attempt) * time.Second
		if n.Backoff != nil {
			pause = n.Backoff(attempt)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
	}
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
