package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
