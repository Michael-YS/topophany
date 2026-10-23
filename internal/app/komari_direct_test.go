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
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

func TestKomariDirectCommandLifecycle(t *testing.T) {
	const key = "synthetic-komari-api-key-canary"
	const uuid = "d4c8d9a1-4ec5-4c1b-9b95-4c1c8f930b0d"
	now := time.Now().UTC()
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/prefix/api/admin/client/list":
			fmt.Fprintf(w, `[{"uuid":%q,"name":"Komari fixture","token":"private-agent-token"}]`, uuid)
		case "/prefix/api/recent/" + uuid:
			fmt.Fprintf(w, `{"status":"success","data":[{"uuid":%q,"updated_at":%q,"cpu":{"usage":25},"ram":{"total":1000,"used":500},"disk":{"total":1000,"used":250}}]}`, uuid, now.Format(time.RFC3339Nano))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer h.Close()
	root := filepath.Join(t.TempDir(), "direct")
	request := map[string]any{"schema": 1, "request_id": "komari-setup", "profile": "desktop", "base_revision": "", "source": map[string]any{"id": "km-example", "name": "Komari", "adapter": "komari", "url": strings.Replace(h.URL, "127.0.0.1", "localhost", 1) + "/prefix", "allow_insecure": true, "interval_seconds": 30}, "auth": map[string]any{"mode": "token", "token": key}, "selected_objects": []string{uuid}}
	run := func(args ...string) []byte {
		t.Helper()
		in, _ := json.Marshal(request)
		var out, diagnostic bytes.Buffer
		a := &App{In: bytes.NewReader(in), Out: &out, Err: &diagnostic, Now: func() time.Time { return now }}
		if err := a.Run(context.Background(), append(args, "--direct-root", root)); err != nil {
			t.Fatalf("%v: %v %s", args, err, out.String())
		}
		if strings.Contains(out.String()+diagnostic.String(), key) || strings.Contains(out.String(), "private-agent-token") {
			t.Fatal("CLI leaked upstream credentials")
		}
		return out.Bytes()
	}
	inspected := run("sources", "inspect", "--json-stdin")
	if !bytes.Contains(inspected, []byte(uuid)) || !bytes.Contains(inspected, []byte(`"allow_insecure":true`)) {
		t.Fatal("node discovery missing", string(inspected))
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspect wrote private storage", err)
	}
	var saved struct {
		Data struct {
			Revision string `json:"profile_revision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(run("sources", "save", "--json-stdin"), &saved); err != nil || saved.Data.Revision == "" {
		t.Fatal("save failed", err)
	}
	if listed := run("sources", "list", "--json"); !bytes.Contains(listed, []byte(`"adapter":"komari"`)) || !bytes.Contains(listed, []byte(`"allow_insecure":true`)) {
		t.Fatal("saved Komari source missing", string(listed))
	}
	var snap model.Snapshot
	if err := json.Unmarshal(run("--source", "direct", "--profile", "desktop", "--json"), &snap); err != nil || len(snap.External) != 1 || snap.External[0].Freshness != "fresh" {
		t.Fatal("no current snapshot", err)
	}
	for _, args := range [][]string{{"status", "--json"}, {"show", "servers", "--json"}, {"show", "servers", "km-example." + uuid, "--json"}} {
		out := run(append(args, "--source", "direct", "--profile", "desktop")...)
		if !bytes.Contains(out, []byte("komari")) || bytes.Contains(out, []byte("ALL CLEAR")) {
			t.Fatal("invalid resource view", string(out))
		}
	}
	text := run("status", "--source", "direct", "--profile", "desktop")
	if !bytes.Contains(text, []byte("Komari direct")) || bytes.Contains(text, []byte("Beszel")) {
		t.Fatal("text view mislabeled", string(text))
	}
	m := tui.Model{Snapshot: snap, Direct: true, Now: now, Width: 140, Height: 40, NoColor: true}
	if rendered := strings.Join(m.Render(), "\n"); !strings.Contains(rendered, "Komari direct") || strings.Contains(rendered, "Beszel direct") {
		t.Fatal("TUI mislabeled Komari")
	}
	manager := stream.NewDirectManager(root, func() time.Time { return now })
	defer manager.Close()
	template := monitor.Template{Schema: 1, Title: "Komari", Rows: []monitor.Row{{ID: "sources", Metric: "sources.status", Visual: "status"}, {ID: "servers", Metric: "servers.list", Visual: "list"}}}
	template.Bar.Row = "sources"
	opened, err := manager.Open(context.Background(), stream.Request{Schema: 1, Source: "direct", Profile: "desktop", ProfileRevision: saved.Data.Revision, Interval: "1s", Template: template})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	deadline := time.Now().Add(2 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		in, err := opened.Fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if in.Snapshot.SourcesStatus != nil && in.Snapshot.SourcesStatus.State == "ready" {
			ready = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ready {
		t.Fatal("Komari monitor stream never became ready")
	}
	request["base_revision"] = saved.Data.Revision
	request["source_id"] = "km-example"
	run("sources", "remove", "--json-stdin")
}
