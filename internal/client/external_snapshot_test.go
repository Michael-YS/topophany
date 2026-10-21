package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestClientReadsValidatedExternalSnapshotProjection(t *testing.T) {
	c := testkit.Config()
	c.Sources = []model.Source{{ID: "probe-api", Adapter: "synthetic-list", Observer: "home", Target: "https://probe.invalid/objects", IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 100, MaxReportBytes: 16 << 10}}
	limit := 90.0
	c.Checks = append(c.Checks, model.Check{ID: "vm-resources", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 120, PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: &limit}}})
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := now.Add(-10 * time.Second)
	cpu := 25.0
	r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", SampleID: "s1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu}}}
	x, _ := config.Check(c, "vm-resources")
	ext := model.NewExternalState()
	ext.Sources["probe-api"] = report.MergeSource(model.SourceState{}, "probe-api", at, true, "", 4)
	state, _ := report.MergeBinding(x.ID, config.Contract(c, x), *x.External, model.BindingState{}, r, at)
	ext.Bindings[x.ID] = state
	snap := engine.NewPlan(c).PrepareExternal(nil, nil, nil, ext).Build("fixture", "", now)
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var got model.Snapshot
	if err := decodeSnapshot(raw, &got); err != nil {
		t.Fatal("valid external snapshot refused", err)
	}
	if len(got.External) != 1 || got.External[0].Report == nil {
		t.Fatalf("external projection lost: %+v", got.External)
	}
	// A schema 2 snapshot cannot smuggle the new projection past an older
	// semantic contract.
	snap.Schema = model.BatchSchemaContracts
	raw, _ = json.Marshal(snap)
	if decodeSnapshot(raw, &got) == nil {
		t.Fatal("schema 2 snapshot accepted external state")
	}
	snap.Schema = model.SnapshotSchema
	snap.External[0].Object = "vm/other"
	raw, _ = json.Marshal(snap)
	if decodeSnapshot(raw, &got) == nil {
		t.Fatal("external view identity mismatch accepted")
	}
	// The report itself is validated, not merely round-tripped.
	snap.External[0].Object = "vm/dmit"
	bad := *snap.External[0].Report
	bad.Resource = nil
	bad.Status = &model.StatusReport{Status: "up"}
	snap.External[0].Report = &bad
	raw, _ = json.Marshal(snap)
	if decodeSnapshot(raw, &got) == nil {
		t.Fatal("wrong external report type accepted")
	}
}

func TestClientStillReadsSchemaTwoNativeSnapshot(t *testing.T) {
	s := engine.Build(testkit.Config(), "fixture", "", nil, nil, time.Now())
	s.Schema = model.BatchSchemaContracts
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got model.Snapshot
	if err := decodeSnapshot(raw, &got); err != nil {
		t.Fatalf("schema 2 native snapshot refused: %v", err)
	}
	if got.Schema != model.BatchSchemaContracts || len(got.External) != 0 {
		t.Fatalf("schema 2 snapshot changed: %+v", got)
	}
}
