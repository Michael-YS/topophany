package app

import (
	"bytes"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"path/filepath"
	"testing"
)

func TestCapacityDetectsMigrationPressureWithoutOpeningData(t *testing.T) {
	c := testkit.Config()
	dir := t.TempDir()
	path := filepath.Join(dir, "topology.json")
	raw, _ := json.Marshal(c)
	os.WriteFile(path, raw, 0600)
	snap := model.Snapshot{Project: c.Project, Storage: &model.StorageUsage{StateBytes: 80 << 20, Observers: []model.StorageObserverUsage{{Observer: "home", Receipts: 1000, Bytes: 80 << 20}}}}
	file := filepath.Join(dir, "snapshot.json")
	raw, _ = json.Marshal(snap)
	os.WriteFile(file, raw, 0600)
	out := &bytes.Buffer{}
	a := &App{Out: out, Err: &bytes.Buffer{}}
	e := a.capacity([]string{"--config", path, "--snapshot", file})
	if exitCode(e) != 2 {
		t.Fatalf("expected preflight warning: %v", e)
	}
	var report capacityReport
	if e = json.Unmarshal(out.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if len(report.Observers) != 2 {
		t.Fatal(report)
	}
	var warned bool
	for _, o := range report.Observers {
		warned = warned || len(o.Warnings) > 0
	}
	if !warned {
		t.Fatal(report)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 2 {
		t.Fatal("preflight wrote state", files)
	}
	c.Observers[0].StorageWeight = 4
	raw, _ = json.Marshal(c)
	os.WriteFile(path, raw, 0600)
	out.Reset()
	if e = a.capacity([]string{"--config", path, "--snapshot", file}); e != nil {
		t.Fatalf("larger reservation should accommodate held state: %v %s", e, out)
	}
}
func TestStorageWeightPreservesMeasurementContracts(t *testing.T) {
	c := testkit.Config()
	before := config.Contracts(c)
	c.Observers[0].StorageWeight = 9
	if config.Contracts(c)[c.Checks[0].ID] != before[c.Checks[0].ID] {
		t.Fatal("quota changed measurement")
	}
	c.Observers[0].StorageWeight = -1
	if config.Validate(c) == nil {
		t.Fatal("negative weight accepted")
	}
}
