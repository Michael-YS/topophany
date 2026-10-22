package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/tui"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func directCommandFixture(t *testing.T) (string, time.Time, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var state, count atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch state.Load() {
		case 1:
			_, _ = fmt.Fprint(w, `{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[]}`)
			return
		case 2:
			w.WriteHeader(403)
			return
		case 3:
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
		_, _ = fmt.Fprintf(w, `{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[{"id":"sssssssssssssss","system":"aaaaaaaaaaaaaaa","type":"1m","created":%q,"stats":{"cpu":25,"mem":[9007199254740993,4503599627370496,0,0],"mp":50,"d":100,"du":25,"dp":25}}]}`, now.Format(time.RFC3339Nano))
	}))
	t.Cleanup(hub.Close)
	root := filepath.Join(t.TempDir(), "direct")
	store, e := direct.NewStore(root)
	if e != nil {
		t.Fatal(e)
	}
	req := direct.Request{Profile: "desktop", Source: direct.SourceConfig{ID: "bs-synthetic", Name: "Synthetic Beszel", Adapter: "beszel", URL: hub.URL, Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}, Selected: []string{"aaaaaaaaaaaaaaa"}}
	if _, _, e = store.Save(context.Background(), req, direct.Credential{Mode: "token", Token: "synthetic-canary-secret", Subject: "users:uuuuuuuuuuuuuuu"}, []direct.System{{ID: "aaaaaaaaaaaaaaa", Name: "Synthetic server"}}); e != nil {
		t.Fatal(e)
	}
	return root, now, &state, &count
}
func TestDirectCurrentStateCommandsKeepTheirShapes(t *testing.T) {
	root, now, state, _ := directCommandFixture(t)
	run := func(args []string, want int) map[string]json.RawMessage {
		t.Helper()
		var out, diagnostic bytes.Buffer
		a := &App{Out: &out, Err: &diagnostic, Now: func() time.Time { return now }}
		e := a.Run(context.Background(), append(args, "--source", "direct", "--profile", "desktop", "--direct-root", root))
		exit := 0
		var ee *ExitError
		if errors.As(e, &ee) {
			exit = ee.Code
		} else if e != nil {
			t.Fatal(e)
		}
		if exit != want {
			t.Fatalf("%v exit %d want %d: %s", args, exit, want, out.String())
		}
		var result map[string]json.RawMessage
		if e = json.Unmarshal(out.Bytes(), &result); e != nil {
			t.Fatal(e, out.String())
		}
		if strings.Contains(out.String()+diagnostic.String(), "canary-secret") {
			t.Fatal("secret leak")
		}
		return result
	}
	snapshot := run([]string{"--json"}, 0)
	if snapshot["topology"] == nil || snapshot["checks"] == nil || snapshot["sources_status"] == nil {
		t.Fatal("not standard Snapshot")
	}
	status := run([]string{"status", "--json"}, 0)
	if status["verdict"] == nil || status["topology"] != nil || status["sources_status"] == nil {
		t.Fatal("changed status shape")
	}
	if bytes.Contains(status["verdict"], []byte("ALL CLEAR")) {
		t.Fatal("claimed network reachability")
	}
	servers := run([]string{"show", "servers", "--json"}, 0)
	if servers["rows"] == nil || servers["tab"] == nil || servers["topology"] != nil {
		t.Fatal("changed view shape")
	}
	if !bytes.Contains(servers["rows"], []byte(`"origin"`)) || !bytes.Contains(servers["rows"], []byte("9007199254740993")) {
		t.Fatal(string(servers["rows"]))
	}
	run([]string{"show", "servers", "bs-synthetic.aaaaaaaaaaaaaaa", "--json"}, 0)
	state.Store(1)
	run([]string{"status", "--json"}, 3)
	state.Store(2)
	run([]string{"status", "--json"}, 4)
	state.Store(3)
	start := time.Now()
	loading := run([]string{"--json", "--wait", "20ms"}, 0)
	if time.Since(start) > 500*time.Millisecond || !bytes.Contains(loading["sources_status"], []byte("loading")) {
		t.Fatal("one-shot wait not bounded")
	}
	var output bytes.Buffer
	a := &App{Out: &output, Err: &output}
	e := a.Run(context.Background(), []string{"history", "completely-unknown-check", "--source", "direct", "--profile", "desktop", "--direct-root", root, "--json"})
	if direct.AsError(e).Code != "history_unsupported_in_direct_mode" {
		t.Fatal(e)
	}
}
func TestDirectMonitorFilesAndTUIProjection(t *testing.T) {
	root, now, _, count := directCommandFixture(t)
	dir := t.TempDir()
	monitorDir := filepath.Join(dir, "panels")
	output := filepath.Join(dir, "metrics.json")
	var out, diagnostic bytes.Buffer
	a := &App{Out: &out, Err: &diagnostic, Now: func() time.Time { return now }}
	if e := a.Run(context.Background(), []string{"monitor", "configure", "desktop", "--source", "direct", "--profile", "desktop", "--direct-root", root, "--monitor-dir", monitorDir, "--output", output, "--interval", "1s"}); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- a.Run(ctx, []string{"monitor", "run", "desktop", "--monitor-dir", monitorDir, "--direct-root", root})
		}()
		deadline := time.Now().Add(3 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			b, e := os.ReadFile(output)
			var p monitor.Panel
			if e == nil && json.Unmarshal(b, &p) == nil && p.Lifecycle == "running" && p.Profile == "desktop" {
				for _, row := range p.Rows {
					if bytes.Contains(mustJSON(row.Value), []byte(`"state":"ready"`)) {
						ready = true
					}
				}
			}
			if ready {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		if e := <-done; e != nil {
			t.Fatal(e)
		}
		if !ready {
			t.Fatal("monitor never ready", diagnostic.String())
		}
		b, e := os.ReadFile(output)
		if e != nil {
			t.Fatal(e)
		}
		var stopped monitor.Panel
		_ = json.Unmarshal(b, &stopped)
		if stopped.Lifecycle != "stopped" {
			t.Fatal("monitor not stopped")
		}
	}
	if count.Load() != 2 {
		t.Fatal("display repeated acquisition", count.Load())
	}
	// The TUI fetch path and its ordinary renderer share the same snapshot.
	f := a.flags("tui-direct")
	_ = a.connection(f)
	mode := addDirectFlags(f)
	f.String("observer", "", "")
	if e := f.Parse([]string{"--source", "direct", "--profile", "desktop", "--direct-root", root}); e != nil {
		t.Fatal(e)
	}
	s, _, e := a.openDirect(context.Background(), f, mode)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a.waitDirect(context.Background(), s, mode)
	snap, server, e := a.directReader(s).snapshot(context.Background(), "")
	if e != nil || server != "direct" {
		t.Fatal(e)
	}
	model := tui.Model{Snapshot: snap, Direct: true, Now: now, Width: 140, Height: 40, NoColor: true}
	rendered := strings.Join(model.Render(), "\n")
	if strings.Contains(rendered, "ALL CLEAR") || !strings.Contains(rendered, view.DirectText("connection")) {
		t.Fatal("TUI resource mode identity missing")
	}
	verdict := (view.View{Snapshot: snap, Now: now}).Verdict()
	if verdict.Text != view.DirectText("ready") {
		t.Fatal(verdict)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
