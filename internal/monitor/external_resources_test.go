package monitor

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestExternalResourceIsFirstServerCardResourceAndExpiresIndependently(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	percent := 25.0
	total, used := uint64(1000), uint64(250)
	valid := now.Add(time.Second)
	check := model.Check{ID: "vm-resources", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "source-a", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: func() *float64 { v := 80.0; return &v }()}}}
	topology := model.Config{Schema: 1, Project: "lab", Nodes: []model.Node{{ID: "dmit", Name: "DMIT"}}, Observers: []model.Observer{{ID: "home", Name: "Home", Node: "home"}}, Checks: []model.Check{check}}
	snap := model.Snapshot{Schema: model.SnapshotSchema, Project: "lab", GeneratedAt: now, Topology: topology, Checks: []model.CheckState{{ID: "vm-resources", Observer: "home", Health: "up", Freshness: "fresh", Evidence: "reported"}}, External: []model.ExternalBindingView{{
		Check: "vm-resources", Source: "source-a", Object: "vm/dmit", ReportType: model.ReportTypeResource, Node: "dmit", Health: "up", Freshness: "fresh", Evidence: "reported", SampledAt: now, TimeBasis: model.TimeBasisUpstreamSample, DataDeadline: now.Add(time.Minute), ContactDeadline: now.Add(time.Minute), ValidUntil: valid,
		Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/dmit", ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}, Disks: []model.DiskReport{{ID: "root", Path: "/", TotalBytes: &total, UsedBytes: &used, Percent: &percent}}}},
	}}}
	cfg := Config{Name: "stream", Source: "preview", Template: Template{Schema: 1, Title: "Topophany", Rows: []Row{{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "dmit"}}}}}
	p := Build(cfg, Input{Snapshot: snap}, now)
	p.PublisherExpiresAt = now.Add(30 * time.Second)
	card, ok := p.Rows[0].Value.(ServerCard)
	if !ok || len(card.Resources) != 1 {
		t.Fatalf("card = %+v", p.Rows[0])
	}
	res := card.Resources[0]
	if res.Check != "vm-resources" || res.Source != "source-a" || res.Selected != true || res.CPU.Percent == nil || *res.CPU.Percent != 25 || res.Disk.Path != "/" || res.ValidUntil == nil || !res.ValidUntil.Equal(valid) {
		t.Fatalf("resource = %+v", res)
	}
	// The card survives its own window; only the nested numbers expire.
	got := Current(p, now.Add(2*time.Second)).Rows[0]
	if got.Value == nil {
		t.Fatal("resource deadline expired the card")
	}
	late := got.Value.(ServerCard).Resources[0]
	if late.CPU.Percent != nil || late.CPU.Logical != nil || late.Memory.Percent != nil || late.Disk.Percent != nil || late.Freshness != "stale" || late.Check != "vm-resources" {
		t.Fatalf("late resource = %+v", late)
	}
}
