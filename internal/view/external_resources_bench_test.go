package view_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func BenchmarkSelectResources512External(b *testing.B) {
	now := time.Now().UTC()
	c := model.Config{Schema: 1, Project: "bench", Checks: []model.Check{}}
	s := model.Snapshot{Schema: model.SnapshotSchema, Project: "bench", GeneratedAt: now, Topology: c}
	cpu := 25.0
	for i := 0; i < 512; i++ {
		node := fmt.Sprintf("node-%03d", i)
		check := fmt.Sprintf("check-%03d", i)
		c.Nodes = append(c.Nodes, model.Node{ID: node})
		c.Observers = append(c.Observers, model.Observer{ID: "obs", Name: "Observer", Node: node})
		c.Checks = append(c.Checks, model.Check{ID: check, Observer: "obs", Kind: "external", External: &model.ExternalBinding{Source: "s", Object: node, Report: model.ReportTypeResource, Node: node, PrimaryDisk: "root"}})
		s.Checks = append(s.Checks, model.CheckState{ID: check, Observer: "obs", Health: "up", Freshness: "fresh", Evidence: "reported"})
		s.External = append(s.External, model.ExternalBindingView{Check: check, Source: "s", Object: node, ReportType: model.ReportTypeResource, Node: node, Health: "up", Freshness: "fresh", Evidence: "reported", SampledAt: now, TimeBasis: model.TimeBasisUpstreamSample, DataDeadline: now.Add(time.Minute), ContactDeadline: now.Add(time.Minute), ValidUntil: now.Add(30 * time.Second), Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: node, ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu}, Disks: []model.DiskReport{{ID: "root", Path: "/", Percent: &cpu}}}}})
	}
	// All 512 checks are deliberately on distinct observers/nodes to keep
	// every selection independent; the slice ordering is the workload.
	s.Topology = c
	v := view.View{Snapshot: s, Now: now}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		got := v.SelectResources()
		if len(got) != 512 {
			b.Fatal(len(got))
		}
	}
}
