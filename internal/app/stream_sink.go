package app

import (
	"context"
	"io"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
)

// A single writer keeps blocked stdout out of acquisition shutdown. Only
// display frames enter it. The final stopped frame gets a bounded write window.
// Real pipe files are joined after interrupting writes with a deadline;
// arbitrary noncancelable embedding Writers remain caller-owned.
func frameSink(ctx context.Context, w io.Writer) (func(monitor.Panel) error, func()) {
	type job struct {
		p   monitor.Panel
		ack chan error
	}
	jobs := make(chan job)
	done := make(chan struct{})
	writerDone := make(chan struct{})
	watcherDone := make(chan struct{})
	deadline, hasDeadline := w.(interface{ SetWriteDeadline(time.Time) error })
	if hasDeadline {
		go func() {
			defer close(watcherDone)
			select {
			case <-done:
				return
			case <-ctx.Done():
				_ = deadline.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
			}
		}()
	} else {
		close(watcherDone)
	}
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-done:
				return
			case j := <-jobs:
				j.ack <- stream.WriteFrame(w, j.p)
			}
		}
	}()
	emit := func(p monitor.Panel) error {
		j := job{p: p, ack: make(chan error, 1)}
		if p.Lifecycle == "stopped" {
			timer := time.NewTimer(250 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				return context.DeadlineExceeded
			case jobs <- j:
			}
			select {
			case <-timer.C:
				return context.DeadlineExceeded
			case e := <-j.ack:
				return e
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case jobs <- j:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-j.ack:
			return e
		}
	}
	stop := func() {
		close(done)
		<-watcherDone
		if hasDeadline {
			_ = deadline.SetWriteDeadline(time.Now())
		}
		select {
		case <-writerDone:
			if hasDeadline {
				_ = deadline.SetWriteDeadline(time.Time{})
			}
		case <-time.After(250 * time.Millisecond):
		}
	}
	return emit, stop
}
