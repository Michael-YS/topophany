package app

import (
	"context"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

func TestMonitorImportUpdateAndExport(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "panels")
	output := filepath.Join(dir, "metrics.json")
	template := `{"schema":1,"title":"Topophany","bar":{"row":"status"},"rows":[{"id":"status","label":"Status","metric":"summary.status","visual":"text"}]}`
	if _, _, e := invoke(template, "monitor", "configure", "desktop", "--monitor-dir", root, "--template", "-", "--source", "collector", "--output", output); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = invoke("", "monitor", "configure", "desktop", "--monitor-dir", root, "--interval", "10s", "--enabled=false"); e != nil {
		t.Fatal(e)
	}
	path, _ := monitor.ConfigPath(root, "desktop")
	c, e := monitor.Load(path)
	if e != nil || c.Interval != "10s" || c.Enabled || c.Template.Title != "Topophany" {
		t.Fatal(c, e)
	}
	after, _ := os.ReadFile(output)
	if string(before) != string(after) {
		t.Fatal("configure reset current output")
	}
	out, _, e := invoke("", "monitor", "export", "desktop", "--monitor-dir", root)
	if e != nil {
		t.Fatal(e)
	}
	var got monitor.Template
	if e = json.Unmarshal([]byte(out), &got); e != nil || got.Title != "Topophany" {
		t.Fatal(out, e)
	}
	if _, _, e = invoke("", "monitor", "run", "desktop", "--monitor-dir", root); e == nil {
		t.Fatal("standalone collector accepted")
	}
}
func TestMonitorInvalidUpdatePreservesConfiguration(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "panels")
	template := `{"schema":1,"title":"Topophany","bar":{"row":"s"},"rows":[{"id":"s","metric":"local.status","visual":"text"}]}`
	if _, _, e := invoke(template, "monitor", "configure", "worker-panel", "--monitor-dir", root, "--template", "-", "--source", "worker", "--output", filepath.Join(dir, "metrics.json")); e != nil {
		t.Fatal(e)
	}
	path, _ := monitor.ConfigPath(root, "worker-panel")
	before, _ := os.ReadFile(path)
	if _, _, e := invoke("", "monitor", "configure", "worker-panel", "--monitor-dir", root, "--interval", "0s"); e == nil {
		t.Fatal("accepted zero interval")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("invalid update changed configuration")
	}
}

func TestMonitorClientPublishesAuthenticatedSnapshot(t *testing.T) {
	dir, c, auth, creds := initialized(t)
	st, e := store.Open(filepath.Join(dir, "data"), store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c)})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	server := httptest.NewServer(api.New(c, "test", st, auth).Handler())
	defer server.Close()
	profile := creds.Profiles[creds.Active]
	profile.Servers[0].URL = server.URL
	creds.Profiles[creds.Active] = profile
	credPath := filepath.Join(dir, "credentials.json")
	if e = client.Save(credPath, creds); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(dir, "panels")
	output := filepath.Join(dir, "metrics.json")
	template := `{"schema":1,"title":"Topophany","bar":{"row":"s"},"rows":[{"id":"s","metric":"summary.status","visual":"text"}]}`
	if _, _, e = invoke(template, "monitor", "configure", "desktop", "--monitor-dir", root, "--template", "-", "--credentials", credPath, "--output", output, "--interval", "1s"); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{Out: io.Discard, Err: io.Discard}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, []string{"monitor", "run", "desktop", "--monitor-dir", root}) }()
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(output)
		var p monitor.Panel
		if json.Unmarshal(b, &p) == nil && len(p.Rows) > 0 && p.Rows[0].Value != nil {
			found = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if !found {
		t.Fatal("authenticated source was never published")
	}
	b, _ := os.ReadFile(output)
	var p monitor.Panel
	json.Unmarshal(b, &p)
	if p.Lifecycle != "stopped" {
		t.Fatal(p)
	}
}

func TestMonitorCollectorAttachment(t *testing.T) {
	dir, _, _, _ := initialized(t)
	root := filepath.Join(dir, "panels")
	output := filepath.Join(dir, "metrics.json")
	template := `{"schema":1,"title":"Topophany","bar":{"row":"s"},"rows":[{"id":"s","metric":"summary.status","visual":"text"}]}`
	if _, _, e := invoke(template, "monitor", "configure", "collector-panel", "--monitor-dir", root, "--template", "-", "--source", "collector", "--output", output, "--interval", "1s"); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	a := &App{Out: io.Discard, Err: io.Discard}
	go func() {
		done <- a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "collector-data"), "--listen", "127.0.0.1:0", "--monitor", "collector-panel", "--monitor-dir", root})
	}()
	found := waitMonitor(output, 3*time.Second, func(p monitor.Panel) bool { return len(p.Rows) > 0 && p.Rows[0].Value != nil })
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if !found {
		t.Fatal("collector attachment did not publish")
	}
}
func waitMonitor(path string, limit time.Duration, accept func(monitor.Panel) bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(path)
		var p monitor.Panel
		if json.Unmarshal(b, &p) == nil && accept(p) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
func TestMonitorWorkerOutputFailureDoesNotStopDurableEnqueue(t *testing.T) {
	dir, c, _, creds := initialized(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			io.WriteString(w, `{"status":"ok"}`)
		} else {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":"fixture_unavailable"}`)
		}
	}))
	defer server.Close()
	c.Checks[0].Target = server.URL + "/probe"
	c.Checks[0].Endpoint = ""
	c.Checks[0].Service = ""
	c.Checks[0].Path = ""
	c.Checks[0].IntervalSeconds = 1
	c.Checks[0].TimeoutSeconds = 1
	saveConfig(t, filepath.Join(dir, "topology.json"), c)
	profile := creds.Profiles[creds.Active]
	profile.Servers[0].URL = server.URL
	creds.Profiles[creds.Active] = profile
	credPath := filepath.Join(dir, "credentials.json")
	if e := client.Save(credPath, creds); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(dir, "panels")
	output := filepath.Join(dir, "metrics.json")
	template := `{"schema":1,"title":"Topophany","bar":{"row":"s"},"rows":[{"id":"s","metric":"check.status","select":{"check":"api-health"},"visual":"text"}]}`
	if _, _, e := invoke(template, "monitor", "configure", "worker-panel", "--monitor-dir", root, "--template", "-", "--source", "worker", "--output", output, "--interval", "1s"); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	data := filepath.Join(dir, "worker-data")
	a := &App{Out: io.Discard, Err: io.Discard}
	go func() {
		done <- a.Run(ctx, []string{"worker", "--config", filepath.Join(dir, "topology.json"), "--credentials", credPath, "--observer", "demo", "--data", data, "--monitor", "worker-panel", "--monitor-dir", root})
	}()
	healthy := waitMonitor(output, 4*time.Second, func(p monitor.Panel) bool { return len(p.Rows) > 0 && p.Rows[0].Value == "up" })
	if !healthy {
		cancel()
		e := <-done
		t.Fatal("local check not published", e)
	}
	pending := filepath.Join(data, "pending")
	before, _ := os.ReadDir(pending)
	backup := filepath.Join(dir, "previous.json")
	if e := os.Rename(output, backup); e != nil {
		cancel()
		<-done
		t.Fatal(e)
	}
	os.Mkdir(output, 0700)
	grew := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		after, _ := os.ReadDir(pending)
		if len(after) > len(before) {
			grew = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	os.Remove(output)
	os.Rename(backup, output)
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if !grew {
		t.Fatal("output failure stopped reliable enqueue")
	}
}
