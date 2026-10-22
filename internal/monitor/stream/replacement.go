package stream

import (
	"context"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

// prepareBudget bounds a replacement's first read while the active request
// keeps publishing. A result returned only because this budget expired is not
// validated evidence.
const prepareBudget = 5 * time.Second

// A candidate owns no runtime until its initial read has passed the project
// guard. During this bounded preparation the old runtime keeps publishing.
// Only one candidate is prepared at a time, preserving request line order.
func prepareReplacement(ctx context.Context, source Source, frames <-chan monitor.Panel, sourceErrors *<-chan *Error, publish Emit, reject func(*Error) error, budget time.Duration) (monitor.Input, *Error, error) {
	var prepareCtx context.Context
	var cancel context.CancelFunc
	if budget > 0 {
		prepareCtx, cancel = context.WithTimeout(ctx, budget)
	} else {
		prepareCtx, cancel = context.WithCancel(ctx)
	}
	type result struct {
		input   monitor.Input
		err     *Error
		expired bool
	}
	prepared := make(chan result, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		in, err := source.Prepare(prepareCtx)
		prepared <- result{in, err, prepareCtx.Err() != nil}
	}()
	defer func() { cancel(); <-done }()
	for {
		select {
		case r := <-prepared:
			if ctx.Err() != nil {
				return monitor.Input{}, nil, nil
			}
			if r.err == nil && r.expired {
				// The budget expired before preparation finished. Commit the
				// candidate as unavailable instead of trusting its partial input.
				return monitor.Input{Unavailable: true}, nil, nil
			}
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
