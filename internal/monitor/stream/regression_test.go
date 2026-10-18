package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestInvalidRequestCannotOverflowErrorFrame(t *testing.T) {
	for _, field := range []string{"request_id", "row_id", "metric"} {
		t.Run(field, func(t *testing.T) {
			r := request()
			r.RequestID = "bad"
			// Keep the wire input below 256 KiB; JSON encoding expands '<'.
			large := strings.Repeat("<", 200000)
			switch field {
			case "request_id":
				r.RequestID = large
			case "row_id":
				r.Template.Rows[0].ID = large
			case "metric":
				r.Template.Rows[0].Metric = large
			}
			var raw bytes.Buffer
			enc := json.NewEncoder(&raw)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(r); err != nil {
				t.Fatal(err)
			}
			if raw.Len() >= MaxRequestBytes {
				t.Fatal("fixture exceeds request budget")
			}
			lines := make(chan Line, 3)
			lines <- line(request())
			lines <- Line{Data: raw.Bytes()}
			r = request()
			r.RequestID = "next"
			lines <- line(r)
			close(lines)
			var sink bytes.Buffer
			if err := Run(context.Background(), lines, OpenSource, func(p monitor.Panel) error { return WriteFrame(&sink, p) }); err != nil {
				t.Fatal(err)
			}
			var frames []monitor.Panel
			for _, raw := range bytes.Split(bytes.TrimSpace(sink.Bytes()), []byte{'\n'}) {
				if len(raw)+1 > MaxFrameBytes {
					t.Fatal("oversized frame")
				}
				var p monitor.Panel
				if err := json.Unmarshal(raw, &p); err != nil {
					t.Fatal(err)
				}
				frames = append(frames, p)
			}
			if len(frames) != 4 || frames[1].Error == nil || frames[1].Error.Code != "invalid_request" || frames[2].RequestID != "next" || frames[3].Lifecycle != "stopped" {
				t.Fatal(frames)
			}
		})
	}
}

func TestForeignCollectorReplacementKeepsPreviousRequest(t *testing.T) {
	for _, scope := range []string{"", "home"} {
		t.Run("observer="+scope, func(t *testing.T) {
			c := testkit.Config()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				snap := engine.Build(c, "fixture", r.URL.Query().Get("observer"), nil, nil, time.Now().UTC())
				// For scoped requests reject the second (complete) snapshot too.
				if r.URL.Query().Get("observer") == "" {
					snap.Project = "foreign"
					snap.Topology.Project = "foreign"
				}
				json.NewEncoder(w).Encode(snap)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "credentials.json")
			if err := client.Save(path, client.Credentials{Schema: 1, Active: "default", Profiles: map[string]client.Profile{"default": {Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: strings.Repeat("a", 64)}}}}}); err != nil {
				t.Fatal(err)
			}
			lines := make(chan Line, 2)
			lines <- line(request())
			r := request()
			r.RequestID = "foreign"
			r.Source = "client"
			r.Lang = ""
			r.Credentials = path
			r.Observer = scope
			lines <- line(r)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var first, stopped monitor.Panel
			rejected, continued := false, false
			err := Run(ctx, lines, OpenSource, func(p monitor.Panel) error {
				if p.Lifecycle == "error" {
					if p.Error.Code != "project_mismatch" || p.RequestID != "foreign" {
						t.Error(p)
					}
					rejected = true
				}
				if p.Lifecycle == "running" {
					if first.Instance == "" {
						first = p
					}
					if p.RequestID != "one" || p.ConfigRevision != first.ConfigRevision {
						t.Error("rejected source committed", p.RequestID)
					}
					if rejected && !continued {
						continued = true
						close(lines)
					}
				}
				if p.Lifecycle == "stopped" {
					stopped = p
				}
				return nil
			})
			if err != nil || !rejected || !continued || stopped.RequestID != "one" || ctx.Err() != nil {
				t.Fatal(err, rejected, continued, stopped.RequestID, ctx.Err())
			}
		})
	}
}

func TestPendingReplacementKeepsPublishingAndJoinsOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lines := make(chan Line, 2)
	lines <- line(request())
	r := request()
	r.RequestID = "pending"
	lines <- line(r)
	var preparing, joined, closed atomic.Bool
	open := func(ctx context.Context, r Request) (Source, *Error) {
		s, e := OpenSource(ctx, request())
		if r.RequestID == "pending" {
			s.Prepare = func(ctx context.Context) (monitor.Input, *Error) {
				preparing.Store(true)
				<-ctx.Done()
				joined.Store(true)
				return monitor.Input{}, nil
			}
			s.Close = func() {
				if !joined.Load() {
					t.Error("closed before preparation joined")
				}
				closed.Store(true)
			}
		}
		return s, e
	}
	continued := false
	err := Run(ctx, lines, open, func(p monitor.Panel) error {
		if p.Lifecycle == "running" && preparing.Load() {
			continued = true
			cancel()
		}
		if p.RequestID == "pending" {
			t.Error("unprepared candidate committed")
		}
		return nil
	})
	if err != nil || !continued || !joined.Load() || !closed.Load() {
		t.Fatal(err, continued, joined.Load(), closed.Load())
	}
}

func TestGoldenNormalizationPreservesDeadlines(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	v := map[string]any{"sampled_at": now.Add(-9 * time.Second).Format(time.RFC3339Nano), "valid_until": now.Add(time.Second).Format(time.RFC3339Nano)}
	normalizeTimes(v, now)
	s, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(v["sampled_at"]))
	u, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(v["valid_until"]))
	if u.Sub(s) != 10*time.Second {
		t.Fatal(v)
	}
}

func TestClientReplacementAcceptsCurrentAndUnavailableSources(t *testing.T) {
	for _, available := range []bool{true, false} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			c := testkit.Config()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !available {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				json.NewEncoder(w).Encode(engine.Build(c, "fixture", "", nil, nil, time.Now().UTC()))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "credentials.json")
			if err := client.Save(path, client.Credentials{Schema: 1, Active: "default", Profiles: map[string]client.Profile{"default": {Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: strings.Repeat("a", 64)}}}}}); err != nil {
				t.Fatal(err)
			}
			r := request()
			r.RequestID = "client"
			r.Source = "client"
			r.Lang = ""
			r.Credentials = path
			lines := make(chan Line, 2)
			lines <- line(request())
			lines <- line(r)
			close(lines)
			var first monitor.Panel
			err := Run(context.Background(), lines, OpenSource, func(p monitor.Panel) error {
				if p.Lifecycle == "error" {
					t.Error(p.Error)
				}
				if p.RequestID == "client" && p.Lifecycle == "running" && first.Instance == "" {
					first = p
				}
				return nil
			})
			if err != nil || first.Instance == "" || first.Project != c.Project || (first.Rows[0].Value != nil) != available {
				t.Fatal(err, first)
			}
		})
	}
}
