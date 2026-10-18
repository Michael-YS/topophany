package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
)

func (a *App) monitorStream(ctx context.Context, args []string) error {
	f := a.flags("monitor stream")
	capabilities := f.Bool("capabilities", false, "print protocol, version, sources, visuals and metrics; then exit")
	if err := parsed(f, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &ExitError{Code: 2, Err: err}
	}
	// stdout is a pipe contract: a closed reader is an ordinary orderly exit.
	signal.Ignore(syscall.SIGPIPE)
	write := func(p monitor.Panel) error { return stream.WriteFrame(a.Out, p) }
	var err error
	if *capabilities {
		err = writeCapabilities(a.Out)
	} else {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		lines := make(chan stream.Line)
		go stream.ReadLines(runCtx, a.In, lines)
		err = stream.Run(runCtx, lines, stream.OpenSource, write)
		cancel()
		if closer, ok := a.In.(io.ReadCloser); ok {
			// An inherited blocking stdin descriptor may also block Close until
			// its writer closes. Shutdown must not wait for that writer.
			go closer.Close()
		}
		// A Reader without Close may be owned by the embedding caller and remain
		// blocked. Its sender still observes cancellation before delivering a line.
	}
	if err != nil && !isStreamClosedPipe(err) {
		fmt.Fprintln(a.Err, "monitor stream output unavailable")
	}
	return nil
}

func isStreamClosedPipe(err error) bool {
	if errors.Is(err, syscall.EPIPE) {
		return true
	}
	// Windows pipe writes return Win32 errors instead of the POSIX EPIPE.
	const (
		errorBrokenPipe syscall.Errno = 109
		errorNoData     syscall.Errno = 232
	)
	return runtime.GOOS == "windows" && (errors.Is(err, errorBrokenPipe) || errors.Is(err, errorNoData))
}

func writeCapabilities(w io.Writer) error {
	return writeJSONLine(w, struct {
		Protocol int              `json:"protocol"`
		Version  string           `json:"version"`
		Sources  []string         `json:"sources"`
		Visuals  []string         `json:"visuals"`
		Metrics  []monitor.Metric `json:"metrics"`
	}{1, model.Version, []string{"client", "preview"}, []string{"text", "bar", "status", "list", "card"}, monitor.Catalog()})
}

// Keep capabilities on the same one-write, one-flush NDJSON sink contract.
func writeJSONLine(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	b = append(b, '\n')
	n, e := w.Write(b)
	if e != nil {
		return e
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if f, ok := w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}
