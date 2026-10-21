package monitor

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func largeResourceSnapshot(nodes int, now time.Time) model.Snapshot {
	topology := model.Config{Schema: 1, Project: "lab"}
	topology.Observers = []model.Observer{{ID: "home", Name: "Home", Node: "home", StaleSeconds: 90}}
	topology.Sources = []model.Source{{ID: "source-a", Adapter: "synthetic-list", Observer: "home", Target: "https://source.invalid/objects", IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: model.MaxSourceObjects, MaxReportBytes: 64 << 10}}
	snap := model.Snapshot{Schema: model.SnapshotSchema, Project: "lab", GeneratedAt: now, Topology: topology}
	percent := 25.0
	for i := 0; i < nodes; i++ {
		id := fmt.Sprintf("node-%03d", i)
		check := model.Check{ID: id + "-res", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "source-a", Object: "obj-" + id, Report: model.ReportTypeResource, Node: id, MaxAgeSeconds: 300, PrimaryDisk: "root"}}
		snap.Topology.Nodes = append(snap.Topology.Nodes, model.Node{ID: id, Name: id})
		snap.Topology.Checks = append(snap.Topology.Checks, check)
		snap.External = append(snap.External, model.ExternalBindingView{
			Check: check.ID, Source: "source-a", Object: check.External.Object, ReportType: model.ReportTypeResource, Node: id,
			Health: "up", Freshness: "fresh", Evidence: "reported", SampledAt: now, TimeBasis: model.TimeBasisUpstreamSample,
			DataDeadline: now.Add(5 * time.Minute), ContactDeadline: now.Add(2 * time.Minute), ValidUntil: now.Add(60 * time.Second),
			Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: check.External.Object, ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}, Disks: []model.DiskReport{{ID: "root", Path: "/", Percent: &percent}}}},
		})
	}
	return snap
}

func resourceRows(n int) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, Row{ID: fmt.Sprintf("row-%03d", i), Metric: "resource.cpu.used_percent", Visual: "bar", Select: Selector{Node: fmt.Sprintf("node-%03d", i)}})
	}
	return rows
}

func BenchmarkResourcePanel512(b *testing.B) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	input := Input{Snapshot: largeResourceSnapshot(512, now)}
	for _, rows := range []int{1, 32, 128} {
		b.Run(fmt.Sprint(rows), func(b *testing.B) {
			cfg := Config{Template: Template{Schema: 1, Title: "resource panel", Rows: resourceRows(rows)}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = Build(cfg, input, now)
			}
		})
	}
}

func TestResourcePanel128RowsAllocationBudget(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	input := Input{Snapshot: largeResourceSnapshot(512, now)}
	cfg := Config{Template: Template{Schema: 1, Title: "resource panel", Rows: resourceRows(128)}}
	allocs := testing.AllocsPerRun(3, func() { _ = Build(cfg, input, now) })
	// The reviewer measured more than 1.2M allocations for this frame. The
	// cached selection removes the per-row selection pass; this threshold
	// leaves generous room for row values and encoding-independent changes.
	if allocs > 120000 {
		t.Fatalf("128-row resource frame allocated %.0f objects", allocs)
	}
}

func TestResourceScalarAndServerCardUseTheSameFrameSelection(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	input := externalPanelInput(now)
	cfg := Config{Template: Template{Schema: 1, Title: "consistent", Rows: []Row{
		{ID: "cpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: Selector{Node: "dmit"}},
		{ID: "card", Metric: "server.card", Visual: "card", Select: Selector{Node: "dmit"}},
	}}}
	panel := Build(cfg, input, now)
	cpu, ok := panel.Rows[0].Value.(float64)
	if !ok || cpu != 25 {
		t.Fatalf("scalar = %#v", panel.Rows[0].Value)
	}
	card, ok := panel.Rows[1].Value.(ServerCard)
	if !ok || len(card.Resources) != 1 || card.Resources[0].CPU.Percent == nil || *card.Resources[0].CPU.Percent != cpu || card.Resources[0].Check != panel.Rows[0].Check {
		t.Fatalf("card and scalar disagree: scalar=%+v card=%+v", panel.Rows[0], card.Resources)
	}
}
