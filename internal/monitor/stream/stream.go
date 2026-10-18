package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

// Source is an owned, prepared source. Open must honor ctx; Fetch follows the
// same five-second deadline contract as monitor.Run. Close releases resources.
type Source struct {
	Errors  <-chan *Error
	Initial monitor.Input
	Fetch   monitor.Fetch
	Close   func()
}
type Open func(context.Context, Request) (Source, *Error)
type Emit func(monitor.Panel) error

// Run serializes request changes, source errors and outgoing frames. Replacing
// a request cancels and joins its runtime before publishing the new revision.
// A rejected line never changes the running source or its cadence.
func Run(ctx context.Context, lines <-chan Line, open Open, emit Emit) error {
	instance := time.Now().UTC().Format("20060102T150405.000000000")
	var sequence, revision uint64
	var active Request
	var c monitor.Config
	var last monitor.Panel
	var digest string
	var frames <-chan monitor.Panel
	var sourceErrors <-chan *Error
	var cancel context.CancelFunc
	var done <-chan struct{}
	var unsubscribe func()
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			unsubscribe()
			cancel = nil
			frames = nil
			sourceErrors = nil
		}
	}
	defer stop()
	send := func(p monitor.Panel) error {
		sequence++
		p.Instance = instance
		p.Sequence = sequence
		p.Name = "stream"
		return emit(p)
	}
	reject := func(id string, e *Error) error {
		now := time.Now().UTC()
		return send(monitor.Panel{Schema: 1, Name: "stream", RequestID: id, Lifecycle: "error", Error: e, GeneratedAt: now, PublisherExpiresAt: now.Add(30 * time.Second), Rows: []monitor.Value{}})
	}
	stopped := func() error {
		stop()
		now := time.Now().UTC()
		p := last
		p.Schema = 1
		p.Name = "stream"
		p.RequestID = active.RequestID
		p.Lifecycle = "stopped"
		p.Error = nil
		p.Rows = []monitor.Value{}
		p.GeneratedAt = now
		p.PublisherExpiresAt = now
		return send(p)
	}
	for {
		select {
		case <-ctx.Done():
			return stopped()
		case line, ok := <-lines:
			if !ok {
				return stopped()
			}
			request, e := Decode(line)
			if e != nil {
				if err := reject(request.RequestID, e); err != nil {
					return err
				}
				continue
			}
			source, e := open(ctx, request)
			if e != nil {
				if err := reject(request.RequestID, e); err != nil {
					return err
				}
				continue
			}
			stop()
			revision++
			active = request
			sourceErrors = source.Errors
			c = monitor.Config{Schema: 1, Name: "stream", Revision: fmt.Sprintf("%s-%d", instance, revision), Source: request.Source, Project: request.Project, Observer: request.Observer, Interval: request.Interval, Template: request.Template}
			runCtx, runCancel := context.WithCancel(ctx)
			hub := &monitor.Hub{}
			ch, unsub, _ := hub.Subscribe()
			ready := make(chan struct{})
			cancel, done, unsubscribe, frames = runCancel, ready, unsub, ch
			// Build the prepared first frame synchronously: even buffered EOF cannot
			// overtake it. Run publishes that same evidence before it starts polling.
			first := monitor.Build(c, source.Initial, time.Now())
			first.RequestID = active.RequestID
			first.PublisherExpiresAt = first.GeneratedAt.Add(30 * time.Second)
			if err := send(first); err != nil {
				runCancel()
				unsub()
				cancel = nil
				if source.Close != nil {
					source.Close()
				}
				return err
			}
			last = first
			digest = monitor.ContentDigest(first)
			go func() {
				defer close(ready)
				if source.Close != nil {
					defer source.Close()
				}
				monitor.Run(runCtx, c, source.Fetch, nil, hub, nil, source.Initial)
			}()
		case e := <-sourceErrors:
			if e != nil {
				if err := reject(active.RequestID, e); err != nil {
					return err
				}
			}
		case p := <-frames:
			if p.Lifecycle != "running" {
				continue
			}
			p.RequestID = active.RequestID
			if monitor.ShouldPublish(last, digest, p, c.Every()) {
				if err := send(p); err != nil {
					return err
				}
				last = p
				digest = monitor.ContentDigest(p)
			}
		}
	}
}

// WriteFrame makes exactly one write, then flushes sinks that support flushing.
// Oversized display values are withheld, preserving the template and deadlines.
func WriteFrame(w interface{ Write([]byte) (int, error) }, p monitor.Panel) error {
	p.Rows = append([]monitor.Value{}, p.Rows...)
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	for len(data)+1 > MaxFrameBytes {
		largest := -1
		size := 0
		for i, r := range p.Rows {
			if r.Value == nil {
				continue
			}
			value, e := json.Marshal(r.Value)
			if e != nil {
				return e
			}
			if len(value) > size {
				largest, size = i, len(value)
			}
		}
		if largest < 0 {
			return fmt.Errorf("frame exceeds 1 MiB")
		}
		r := &p.Rows[largest]
		r.Value = nil
		r.Text = "unknown"
		r.Health = "unknown"
		r.Freshness = "unavailable"
		r.Reason = "frame_budget_exceeded"
		data, err = json.Marshal(p)
		if err != nil {
			return err
		}
	}
	data = append(data, '\n')
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("short frame write")
	}
	if f, ok := w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}
