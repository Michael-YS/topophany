package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/preview"
)

func TestStreamReplacementErrorsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan Line)
	frames := make(chan monitor.Panel, 64)
	done := make(chan error, 1)
	var opened, closed atomic.Int32
	open := func(ctx context.Context, r Request) (Source, *Error) {
		opened.Add(1)
		return Source{Initial: monitor.Input{Unavailable: true}, Fetch: func(ctx context.Context) (monitor.Input, error) { <-ctx.Done(); return monitor.Input{}, ctx.Err() }, Close: func() { closed.Add(1) }}, nil
	}
	go func() { done <- Run(ctx, lines, open, func(p monitor.Panel) error { frames <- p; return nil }) }()
	r := request()
	lines <- line(r)
	first := wait(t, frames)
	if first.RequestID != "one" || first.Lifecycle != "running" {
		t.Fatal(first)
	}
	lines <- Line{Data: []byte("{")}
	invalid := wait(t, frames)
	if invalid.Lifecycle != "error" || invalid.Error.Code != "invalid_request" {
		t.Fatal(invalid)
	}
	r.RequestID = "two"
	lines <- line(r)
	next := wait(t, frames)
	if next.RequestID != "two" || next.ConfigRevision == first.ConfigRevision || next.Instance != first.Instance || next.Sequence <= invalid.Sequence {
		t.Fatal(next)
	}
	if closed.Load() != 1 {
		t.Fatal("replacement did not join source", closed.Load())
	}
	cancel()
	stopped := wait(t, frames)
	if stopped.Lifecycle != "stopped" || stopped.RequestID != "two" || len(stopped.Rows) != 0 {
		t.Fatal(stopped)
	}
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	if opened.Load() != 2 || closed.Load() != 2 {
		t.Fatal(opened.Load(), closed.Load())
	}
}
func TestStreamEOFAndClosedSink(t *testing.T) {
	for _, fail := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		lines := make(chan Line, 1)
		lines <- line(request())
		close(lines)
		var frames []monitor.Panel
		sentinel := context.DeadlineExceeded
		err := Run(ctx, lines, OpenSource, func(p monitor.Panel) error {
			frames = append(frames, p)
			if fail {
				return sentinel
			}
			return nil
		})
		cancel()
		if fail {
			if err != sentinel || len(frames) != 1 {
				t.Fatal(err, frames)
			}
			continue
		}
		if err != nil || len(frames) != 2 || frames[0].Rows[0].Value == nil || frames[1].Lifecycle != "stopped" || len(frames[1].Rows) != 0 {
			t.Fatal(err, frames)
		}
	}
}

func TestPreviewGoldenFrames(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	r := request()
	r.Template.Rows = append(r.Template.Rows,
		monitor.Row{ID: "observers", Metric: "observers.verdicts", Visual: "list"},
		monitor.Row{ID: "attention", Metric: "attention.list", Visual: "list"},
		monitor.Row{ID: "applications", Metric: "applications.list", Visual: "list"},
		monitor.Row{ID: "server", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "app"}},
	)
	config, e := preview.Preset("en")
	if e != nil {
		t.Fatal(e)
	}
	scenarios := preview.New(config, now, "en").ScenarioIDs()
	for sc, name := range scenarios {
		t.Run(name, func(t *testing.T) {
			r.Scenario = sc
			source, err := openSource(context.Background(), r, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			p := monitor.Build(monitor.Config{Name: "stream", Revision: "normalized", Source: "preview", Template: r.Template}, source.Initial, now)
			p.Instance = "normalized"
			p.Sequence = 1
			p.RequestID = "normalized"
			p.PublisherExpiresAt = now.Add(30 * time.Second)
			sink := &frameSink{}
			if err := WriteFrame(sink, p); err != nil {
				t.Fatal(err)
			}
			var generic any
			if err := json.Unmarshal(sink.Bytes(), &generic); err != nil {
				t.Fatal(err)
			}
			// Normalize every RFC3339 timestamp, including nested resources/incidents.
			normalizeTimes(generic)
			data, err2 := json.Marshal(generic)
			if err2 != nil {
				t.Fatal(err2)
			}
			data = append(data, '\n')
			path := filepath.Join("..", "testdata", fmt.Sprintf("preview-%02d.ndjson", sc))
			if os.Getenv("UPDATE_MONITOR_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if string(data) != string(want) {
				t.Fatalf("frame differs from %s", path)
			}
		})
	}
}
func normalizeTimes(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if s, ok := v.(string); ok {
				if _, e := time.Parse(time.RFC3339Nano, s); e == nil && strings.Contains(s, "T") {
					x[k] = "2000-01-01T00:00:00Z"
					continue
				}
			}
			normalizeTimes(v)
		}
	case []any:
		for _, v := range x {
			normalizeTimes(v)
		}
	}
}
