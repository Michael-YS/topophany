package app

import (
	"context"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/store"
	"io"
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
