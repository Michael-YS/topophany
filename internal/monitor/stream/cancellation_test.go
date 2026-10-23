package stream

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

func TestCancellationDuringEmitStillStopsAndJoinsSource(t *testing.T) {
	for _, during := range []string{"first frame", "rejected request"} {
		t.Run(during, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lines := make(chan Line, 2)
			lines <- line(request())
			if during == "rejected request" {
				lines <- Line{Data: []byte("{")}
			}
			var closed atomic.Int32
			open := func(context.Context, Request) (Source, *Error) {
				return Source{
					Initial: monitor.Input{Unavailable: true},
					Fetch: func(ctx context.Context) (monitor.Input, error) {
						<-ctx.Done()
						return monitor.Input{}, ctx.Err()
					},
					Close: func() { closed.Add(1) },
				}, nil
			}
			var frames []monitor.Panel
			err := Run(ctx, lines, open, func(p monitor.Panel) error {
				frames = append(frames, p)
				if p.Lifecycle == "stopped" {
					if closed.Load() != 1 {
						t.Error("stopped frame preceded source cleanup")
					}
					return nil
				}
				if during == "first frame" || p.Lifecycle == "error" {
					// Model a completed pipe write whose acknowledgment races SIGTERM.
					cancel()
					return ctx.Err()
				}
				return nil
			})
			want := 2
			if during == "rejected request" {
				want = 3
			}
			if err != nil || len(frames) != want || closed.Load() != 1 {
				t.Fatalf("err=%v, frames=%d, closed=%d", err, len(frames), closed.Load())
			}
			last := frames[len(frames)-1]
			if last.Lifecycle != "stopped" || last.RequestID != "one" || last.Name != "stream" || len(last.Rows) != 0 || last.Error != nil || last.PublisherExpiresAt != last.GeneratedAt {
				t.Fatal("invalid final frame", last)
			}
			if last.Instance != frames[0].Instance || last.ConfigRevision != frames[0].ConfigRevision || last.Source != frames[0].Source || last.Project != frames[0].Project || last.Sequence != frames[len(frames)-2].Sequence+1 {
				t.Fatal("final frame lost stream/config identity or sequence")
			}
		})
	}
}

func TestCanceledStreamDoesNotRetryFailedFinalFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var attempts int
	err := Run(ctx, make(chan Line), nil, func(p monitor.Panel) error {
		attempts++
		if p.Lifecycle != "stopped" || len(p.Rows) != 0 {
			t.Fatal("unexpected frame", p)
		}
		return io.ErrClosedPipe
	})
	if !errors.Is(err, io.ErrClosedPipe) || attempts != 1 {
		t.Fatal("final write failure was retried or hidden", err, attempts)
	}
}

func TestCancellationDoesNotHideUnrelatedOutputFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan Line, 1)
	lines <- line(request())
	var attempts int
	open := func(context.Context, Request) (Source, *Error) {
		return Source{Initial: monitor.Input{Unavailable: true}}, nil
	}
	err := Run(ctx, lines, open, func(monitor.Panel) error {
		attempts++
		cancel()
		return io.ErrClosedPipe
	})
	if !errors.Is(err, io.ErrClosedPipe) || attempts != 1 {
		t.Fatal("unrelated output error was hidden", err, attempts)
	}
}
