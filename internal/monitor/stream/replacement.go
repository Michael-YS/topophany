package stream

import (
	"context"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

// A candidate owns no runtime until its initial read has passed the project
// guard. During this bounded preparation the old runtime keeps publishing.
// Only one candidate is prepared at a time, preserving request line order.
func prepareReplacement(ctx context.Context, source Source, frames <-chan monitor.Panel, sourceErrors *<-chan *Error, publish Emit, reject func(*Error) error) (monitor.Input, *Error, error) {
	prepareCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	type result struct {
		input monitor.Input
		err   *Error
	}
	prepared := make(chan result, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		in, err := source.Prepare(prepareCtx)
		prepared <- result{in, err}
	}()
	defer func() { cancel(); <-done }()
	for {
		select {
		case r := <-prepared:
			return r.input, r.err, nil
		case p := <-frames:
			if err := publish(p); err != nil {
				return monitor.Input{}, nil, err
			}
		case e, ok := <-*sourceErrors:
			if !ok {
				*sourceErrors = nil
			} else if e != nil {
				if err := reject(e); err != nil {
					return monitor.Input{}, nil, err
				}
			}
		case <-ctx.Done():
			return monitor.Input{}, nil, nil
		}
	}
}
