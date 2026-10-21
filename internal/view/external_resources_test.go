package view_test

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func externalSelectionFixture(t *testing.T) (model.Config, model.Snapshot, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	c := model.Config{Schema: 1, Project: "lab", Nodes: []model.Node{{ID: "dmit", Name: "DMIT", ResourceChecks: []string{"primary", "fallback"}}}, Observers: []model.Observer{{ID: "home", Name: "Home", Node: "home"}}, Checks: []model.Check{
		{ID: "primary", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "source-a", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: ptr(80)}}},
		{ID: "fallback", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "source-b", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", PrimaryDisk: "data"}},
	}}
	snap := model.Snapshot{Schema: model.SnapshotSchema, Project: "lab", GeneratedAt: now, Topology: c, Checks: []model.CheckState{
		{ID: "primary", Observer: "home", Contract: strings.Repeat("a", 64), Health: "up", Freshness: "fresh", Evidence: "reported", Last: nil, Availability: []model.Availability{}},
		{ID: "fallback", Observer: "home", Contract: strings.Repeat("b", 64), Health: "up", Freshness: "fresh", Evidence: "reported", Last: nil, Availability: []model.Availability{}},
		{ID: "native", Observer: "home", Health: "up", Freshness: "fresh", Evidence: "reported", Last: nil},
	}}
	p := 25.0
	ctrl := 80.0
	snap.External = []model.ExternalBindingView{
		{Check: "primary", Source: "source-a", Object: "vm/dmit", ReportType: model.ReportTypeResource, Node: "dmit", Health: "up", Freshness: "fresh", Evidence: "reported", SampledAt: now.Add(-time.Second), TimeBasis: model.TimeBasisUpstreamSample, DataDeadline: now.Add(time.Minute), ContactDeadline: now.Add(time.Minute), ValidUntil: now.Add(30 * time.Second), Reason: "external resource reading is current",
			Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", ObservedAt: now.Add(-time.Second), TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &p, LogicalCores: ptrInt(8)}, Memory: &model.MemoryReport{TotalBytes: ptrU(100), UsedBytes: ptrU(25), Percent: &p}, Disks: []model.DiskReport{{ID: "root", Path: "/", TotalBytes: ptrU(200), UsedBytes: ptrU(50), Percent: &ctrl}}}}},
		{Check: "fallback", Source: "source-b", Object: "vm/dmit", ReportType: model.ReportTypeResource, Node: "dmit", Health: "up", Freshness: "fresh", Evidence: "reported", SampledAt: now.Add(-time.Second), TimeBasis: model.TimeBasisUpstreamSample, DataDeadline: now.Add(time.Minute), ContactDeadline: now.Add(time.Minute), ValidUntil: now.Add(30 * time.Second), Reason: "external resource reading is current",
			Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", ObservedAt: now.Add(-time.Second), TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: ptr(99)}, Disks: []model.DiskReport{{ID: "data", Path: "/data", TotalBytes: ptrU(300), UsedBytes: ptrU(30), Percent: &p}}}}},
	}
	return c, snap, now
}

func ptr(v float64) *float64 { return &v }
func ptrInt(v int) *int      { return &v }
func ptrU(v uint64) *uint64  { return &v }

func TestSelectResourcesPrimaryOrderedFallbackAndDiskIdentity(t *testing.T) {
	_, snap, now := externalSelectionFixture(t)
	v := view.View{Snapshot: snap, Now: now}
	all := v.SelectResources()
	n, ok := all["dmit"]
	if !ok || n.Selected.Check != "primary" || len(n.All) != 1 || !n.Selected.Selected || n.Selected.Source != "source-a" {
		t.Fatalf("primary selection = %+v", n)
	}
	if n.Selected.DiskPath != "/" || n.Selected.Facts["disk_used_percent"] != "80.00" || n.Selected.Facts["cpu_logical_count"] != "8" {
		t.Fatalf("primary facts = %+v", n.Selected)
	}
	if vals := view.ResourceValues(n.Selected); vals[2][1] == "unknown" {
		t.Fatal("primary disk not summarized")
	}
	// An expired primary falls back to the ordered second check. Partial
	// fields in the fallback do not change that rule.
	snap.External[0].Freshness = "stale"
	n = v.SelectResources()["dmit"]
	if n.Selected.Check != "fallback" || !strings.Contains(n.Reason, "fallback") || len(n.All) != 1 {
		t.Fatalf("fallback selection = %+v", n)
	}
	// A current primary with a missing configured disk stays selected and
	// reports its disk unknown; it does not borrow the fallback's volume.
	snap.External[0].Freshness = "fresh"
	snap.External[0].Report.Resource.Disks = nil
	n = v.SelectResources()["dmit"]
	if n.Selected.Check != "primary" || n.Selected.Facts == nil {
		t.Fatalf("partial primary = %+v", n)
	}
	if _, ok := n.Selected.Facts["disk_used_percent"]; ok {
		t.Fatal("missing primary disk invented a value")
	}
	if len(n.All) != 1 || n.All[0].Check != "primary" {
		t.Fatal("partial primary triggered cross-source stitching")
	}
	// With no usable report, the primary is shown unknown/expired rather
	// than silently promoting an unauthorized value.
	snap.External[0].Freshness = "stale"
	snap.External[1].Freshness = "stale"
	n = v.SelectResources()["dmit"]
	if n.Selected.Check != "primary" || n.Selected.Health != "unknown" || n.Selected.Facts != nil {
		t.Fatalf("no-current selection = %+v", n)
	}
}

func TestSelectResourcesPreservesNativeOnlyBehavior(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	c := model.Config{Schema: 1, Project: "lab", Nodes: []model.Node{{ID: "host"}}, Observers: []model.Observer{{ID: "home", Name: "Home", Node: "host"}}, Checks: []model.Check{
		{ID: "host-a", Observer: "home", Kind: "host", StaleSeconds: 90, Host: &model.HostOptions{DiskPath: "/"}},
		{ID: "host-b", Observer: "home", Kind: "host", StaleSeconds: 90, Host: &model.HostOptions{DiskPath: "/data"}},
	}}
	last := func(id, path string) *model.Observation {
		return &model.Observation{ID: id, Check: id, Observer: "home", SampledAt: now.Add(-time.Second), Outcome: "ok", Evidence: "reported", Facts: map[string]string{"cpu_used_percent": "10.00", "memory_total_bytes": "100", "memory_used_bytes": "20", "memory_used_percent": "20.00", "disk_path": path, "disk_total_bytes": "200", "disk_used_bytes": "50", "disk_used_percent": "25.00"}}
	}
	snap := model.Snapshot{Schema: model.SnapshotSchema, Project: "lab", GeneratedAt: now, Topology: c, Checks: []model.CheckState{
		{ID: "host-a", Observer: "home", Health: "up", Freshness: "fresh", Evidence: "reported", Last: last("host-a", "/")},
		{ID: "host-b", Observer: "home", Health: "up", Freshness: "fresh", Evidence: "reported", Last: last("host-b", "/data")},
	}}
	n := view.View{Snapshot: snap, Now: now}.SelectResources()["host"]
	if n.Selected.Check != "host-a" || len(n.All) != 2 || !n.Selected.Native || n.All[0].Check != "host-a" || n.All[1].Check != "host-b" {
		t.Fatalf("native selection = %+v", n)
	}
	if n.Selected.Facts["memory_used_bytes"] != "20" || n.Selected.Facts["disk_used_percent"] != "25.00" {
		t.Fatalf("native conversion changed facts: %+v", n.Selected.Facts)
	}
	if got := view.ResourceOptions(model.Check{External: &model.ExternalBinding{Assertions: &model.ResourceAssertions{CPUPercentMax: ptr(70), MemoryPercentMax: ptr(80), DiskPercentMax: ptr(90)}}}); got.MaxCPUPercent != 70 || got.MaxMemoryPercent != 80 || got.MaxDiskPercent != 90 {
		t.Fatalf("external limits = %+v", got)
	}
}
