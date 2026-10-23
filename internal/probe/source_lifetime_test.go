package probe

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

type stalledTLSAdapter struct{}

func (stalledTLSAdapter) Validate(model.Source) error { return nil }
func (stalledTLSAdapter) Collect(ctx context.Context, transport source.IO, src model.Source, _ []source.Request) ([]source.Reading, error) {
	_, err := transport.Get(ctx, src, "", nil, 1024)
	return nil, err
}

func TestSourceCancellationClosesUnfinishedTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn)
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			select {
			case accepted <- c:
			case <-stopped:
				c.Close()
				return
			}
		}
	}()
	rt := NewSourceRuntime(map[string]source.Adapter{"stall": stalledTLSAdapter{}})
	src := model.Source{ID: "audit", Adapter: "stall", Target: "https://" + ln.Addr().String(), TimeoutSeconds: 30, MaxObjects: 1, MaxPages: 1, MaxResponseBytes: 1024}
	for range 6 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		done := make(chan error, 1)
		go func() {
			_, e := rt.Collect(ctx, model.Config{}, src, []source.Request{{Object: "test", Reports: []string{model.ReportTypeResource}}})
			done <- e
		}()
		var conn net.Conn
		select {
		case conn = <-accepted:
		case <-ctx.Done():
			cancel()
			t.Fatal("no connection")
		}
		// Read ClientHello before canceling, proving the detached dial is active.
		conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 8192)
		if _, e := conn.Read(buf); e != nil {
			conn.Close()
			cancel()
			t.Fatal(e)
		}
		cancel()
		if e := <-done; e == nil {
			conn.Close()
			t.Fatal("canceled read succeeded")
		}
		conn.SetReadDeadline(time.Now().Add(time.Second))
		_, e := io.Copy(io.Discard, conn)
		conn.Close()
		if e != nil {
			t.Fatalf("attempt retained TLS socket after cancellation: %v", e)
		}
	}
}
