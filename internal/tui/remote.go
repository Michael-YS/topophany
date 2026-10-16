//go:build !js || collector

package tui

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Remote is the native read-only viewer driven by browser events. Only this
// owner's calls mutate its Model; background reads return through bounded
// channels and never block keyboard handling or frame rendering.
type Remote struct {
	m         *Model
	client    *client.Client
	ctx       context.Context
	cancel    context.CancelFunc
	results   chan remoteSnapshot
	samples   sampler
	encoder   cellEncoder
	requested time.Time
	painted   time.Time
	busy      bool
	closed    bool
}

type remoteSnapshot struct {
	observer string
	snapshot model.Snapshot
	server   string
	err      error
	took     time.Duration
}

func NewRemote(profile client.Profile, reconnectHint string) (*Remote, error) {
	c, err := client.New(profile)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{Collectors: c.URLs(), Project: c.Project(), Colors: colorTrue,
		Now: time.Now(), Loading: "connecting to the collector", NerdFont: true}
	m.initializeStartup(nil)
	m.ReconnectHint = secure.Text(reconnectHint[:min(len(reconnectHint), 1024)])
	m.NerdFont = true
	r := &Remote{m: m, client: c, ctx: ctx, cancel: cancel, results: make(chan remoteSnapshot, 1)}
	r.samples = sampler{results: make(chan fetchedSamples, 1), read: func(ctx context.Context, check string, limit int) ([]model.Observation, error) {
		h, _, err := c.History(ctx, check, limit)
		return h.Entries, err
	}}
	return r, nil
}

func (r *Remote) Close() {
	if !r.closed {
		r.closed = true
		r.cancel()
		r.client.Close()
	}
}

func (r *Remote) Key(key string, now time.Time) {
	if r.closed {
		return
	}
	r.m.Now = now
	switch r.m.Key(key) {
	case "quit":
		r.Close()
	case "refresh":
		r.requested = time.Time{}
	case "view":
		r.m.NextObserver()
		r.m.Snapshot = model.Snapshot{Topology: r.m.Snapshot.Topology}
		r.m.Error, r.m.Loading = "", "loading the observer view"
		r.requested = time.Time{}
	}
}

func (r *Remote) request(now time.Time) {
	if r.busy || r.closed {
		return
	}
	r.busy, r.m.Fetching, r.requested = true, true, now
	observer := r.m.Observer
	go func() {
		start := time.Now()
		snap, server, err := r.client.Snapshot(r.ctx, observer)
		select {
		case r.results <- remoteSnapshot{observer, snap, server, err, time.Since(start)}:
		case <-r.ctx.Done():
		}
	}()
}

func (r *Remote) receive() bool {
	changed := false
	select {
	case got := <-r.results:
		r.busy, r.m.Fetching, changed = false, false, true
		if got.observer != r.m.Observer {
			r.requested = time.Time{}
			break
		}
		r.m.Loading = ""
		r.m.perf.fetch, r.m.perf.fetches = got.took, r.m.perf.fetches+1
		if got.err != nil {
			r.m.Error = got.err.Error()
		} else {
			r.m.Snapshot, r.m.Server, r.m.Error = got.snapshot, got.server, ""
		}
		r.samples.request(r.ctx, r.m, true)
	default:
	}
	select {
	case got := <-r.samples.results:
		r.samples.busy, changed = false, true
		if got.trend {
			r.m.KeepTrend(got.check, got.entries, got.err)
		} else {
			r.m.KeepSamples(got.check, got.entries, got.err)
		}
	default:
	}
	return changed
}

func (r *Remote) Frame(now time.Time, cols, rows int, force bool) *DemoFrame {
	m := r.m
	cols, rows = min(240, max(30, cols)), min(90, max(10, rows))
	resized := m.Width != cols || m.Height != rows
	m.Width, m.Height, m.Now = cols, rows, now
	changed := false
	if !r.closed {
		changed = r.receive()
		if r.requested.IsZero() || now.Sub(r.requested) >= retryEvery {
			r.request(now)
		}
		r.samples.request(r.ctx, m, false)
	}
	if !force && !resized && !changed && (r.closed || now.Sub(r.painted) < m.Animating()) {
		return nil
	}
	start := time.Now()
	cv := m.draw()
	if len(r.encoder.styles) > 8192 {
		r.encoder = cellEncoder{}
	}
	f := &DemoFrame{CellFrame: r.encoder.encode(cv), Title: m.title(), Observer: m.Observer,
		Tab: m.Tab, Searching: m.Searching, Closed: r.closed}
	if force || resized || changed {
		f.Text = strings.Join(cv.Lines(colorNone), "\n")
	}
	r.painted = now
	encoded, _ := json.Marshal(f.CellFrame)
	m.measured(time.Since(start), len(encoded), now)
	return f
}
