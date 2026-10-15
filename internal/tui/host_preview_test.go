package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/preview"
)

func TestHostPreviewRendersResourcesAndScenarioMenu(t *testing.T) {
	c, err := config.Load("../../examples/host-resources.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sim := preview.New(c, now, "zh")
	sim.SetScenario(slices.Index(sim.ScenarioIDs(), "host-cpu-high"), now.Add(-5*time.Minute))
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{Snapshot: snap, Now: now, Width: 180, Height: 80, Preview: sim, Server: "synthetic", NoColor: true}
	m.Key("6")
	m.Key("enter")
	out := visible(m)
	for _, key := range []string{"cpu_used_percent", "memory_used_percent", "disk_used_percent", "process_matching_count", "host_resource_threshold"} {
		if !strings.Contains(out, key) {
			t.Fatal("resource evidence missing", key, out)
		}
	}
	m.Key("esc")
	m.Key("x")
	out = visible(m)
	for _, name := range []string{"主机 CPU 高占用", "主机内存紧张", "主机磁盘将满", "主机进程缺失"} {
		if !strings.Contains(out, name) {
			t.Fatal("scenario missing", name, out)
		}
	}
}

func TestFullPreviewServerCardsDisplayHostResources(t *testing.T) {
	m := previewModel(t)
	m.Key("3")
	out := visible(m)
	for _, text := range []string{"LOCAL RESOURCES", "CPU", "MEM", "DISK", "CPUs", "GiB", "processes"} {
		if !strings.Contains(out, text) {
			t.Fatal("resource card missing", text, out)
		}
	}
	f := m.fleet()
	count := 0
	for _, node := range m.Snapshot.Topology.Nodes {
		card := f.Card(node.ID)
		if node.Kind == "host" || node.Kind == "router" {
			count++
			if len(card.Resources) != 1 {
				t.Fatal("host card has no resource sample", node.ID, card.Resources)
			}
		}
	}
	if count != 11 {
		t.Fatal(count)
	}
	m.Key("goto:check:host-win")
	if row, ok := m.selectedRow(); !ok || m.Tab != tabChecks || row.ID != "host-win" {
		t.Fatal("resource row did not open its check", row)
	}
	m.Error = "collector unavailable"
	for _, resource := range m.fleet().Card("app").Resources {
		if resource.Health != "unknown" || len(resource.Facts) != 0 {
			t.Fatal("cached resource retained live values", resource)
		}
	}
}
