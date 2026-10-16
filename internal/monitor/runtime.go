package monitor

import (
	"context"
	"sync"
	"time"
)

type Fetch func(context.Context) (Input, error)

// Run isolates fetches and file writes from the sampling/ingestion path. All
// queues have capacity one; only display frames may be coalesced.
func Run(ctx context.Context, c Config, fetch Fetch, out *Output, hub *Hub, report func(string)) {
	if hub == nil {
		hub = &Hub{}
	}
	if report == nil {
		report = func(string) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	incoming := make(chan Input, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(min(c.Every(), 5*time.Second))
		defer tick.Stop()
		for {
			request, done := context.WithTimeout(ctx, 5*time.Second)
			in, e := fetch(request)
			done()
			if e != nil {
				in.Unavailable = true
			}
			select {
			case <-incoming:
			default:
			}
			select {
			case incoming <- in:
			case <-ctx.Done():
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
	frames, unsubscribe, _ := hub.Subscribe()
	wg.Go(func() {
		defer unsubscribe()
		failed := false
		write := func(p Panel, force bool) {
			if e := out.Write(p, c.Every(), force); e != nil {
				if !failed {
					report("monitor output unavailable; retrying")
				}
				failed = true
			} else if failed {
				report("monitor output recovered")
				failed = false
			}
		}
		for {
			select {
			case <-ctx.Done():
				if p, ok := hub.Latest(); ok {
					p.Sequence++
					p.GeneratedAt = time.Now().UTC()
					p.Lifecycle = "stopped"
					write(p, true)
				}
				return
			case p := <-frames:
				write(p, false)
			}
		}
	})
	instance := time.Now().UTC().Format("20060102T150405.000000000")
	var sequence uint64
	in := Input{Unavailable: true}
	publish := func() {
		sequence++
		p := Build(c, in, time.Now())
		p.Instance = instance
		p.Sequence = sequence
		p.PublisherExpiresAt = p.GeneratedAt.Add(30 * time.Second)
		hub.Publish(p)
	}
	publish()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case in = <-incoming:
			publish()
		case <-tick.C:
			publish()
		}
	}
}
