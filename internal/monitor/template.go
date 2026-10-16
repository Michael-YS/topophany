// Package monitor projects evidence into bounded, versioned display snapshots.
// It never schedules probes or changes evidence persistence.
package monitor

import (
	"errors"
	"fmt"
	"slices"

	"github.com/WhitenWhiten/topophany/internal/config"
)

type Template struct {
	Schema int    `json:"schema"`
	Title  string `json:"title"`
	Icon   string `json:"icon,omitempty"`
	Bar    struct {
		Row string `json:"row"`
	} `json:"bar"`
	Rows []Row `json:"rows"`
}
type Row struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Metric string   `json:"metric"`
	Select Selector `json:"select,omitempty"`
	Format string   `json:"format,omitempty"`
	Visual string   `json:"visual"`
}
type Selector struct {
	Check     string `json:"check,omitempty"`
	Collector string `json:"collector,omitempty"`
}
type Metric struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Unit     string   `json:"unit,omitempty"`
	Sources  []string `json:"sources"`
	Selector string   `json:"selector,omitempty"`
	Fact     string   `json:"-"`
}

func Catalog() []Metric {
	all := []string{"client", "collector", "worker"}
	return []Metric{
		{Name: "summary.status", Type: "text", Sources: []string{"client", "collector"}},
		{Name: "local.status", Type: "text", Sources: []string{"worker"}},
		{Name: "check.status", Type: "text", Sources: all, Selector: "check"},
		{Name: "host.cpu.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "check", Fact: "cpu_used_percent"},
		{Name: "host.memory.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "check", Fact: "memory_used_percent"},
		{Name: "host.disk.used_percent", Type: "number", Unit: "percent", Sources: all, Selector: "check", Fact: "disk_used_percent"},
		{Name: "delivery.status", Type: "text", Sources: []string{"worker"}, Selector: "collector"},
		{Name: "delivery.pending_records", Type: "number", Unit: "count", Sources: []string{"worker"}, Selector: "collector"},
		{Name: "delivery.pending_bytes", Type: "number", Unit: "bytes", Sources: []string{"worker"}, Selector: "collector"},
		{Name: "storage.journal.write_bytes", Type: "number", Unit: "bytes", Sources: []string{"collector"}},
		{Name: "storage.checkpoint.write_bytes", Type: "number", Unit: "bytes", Sources: []string{"collector"}},
		{Name: "storage.queue.operations", Type: "number", Unit: "count", Sources: []string{"worker"}},
		{Name: "storage.queue.write_bytes", Type: "number", Unit: "bytes", Sources: []string{"worker"}},
		{Name: "storage.sync.count", Type: "number", Unit: "count", Sources: []string{"collector", "worker"}},
		{Name: "storage.sync.duration_ms", Type: "number", Unit: "milliseconds", Sources: []string{"collector", "worker"}},
	}
}
func metric(name string) (Metric, bool) {
	for _, m := range Catalog() {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}
func (t Template) Validate(source string) error {
	if !slices.Contains([]string{"client", "collector", "worker"}, source) {
		return errors.New("unknown monitor source")
	}
	if t.Schema != 1 || len(t.Title) == 0 || len(t.Title) > 256 || len(t.Icon) > 64 || len(t.Rows) == 0 || len(t.Rows) > 128 {
		return errors.New("invalid template schema, title, icon or row count (1..128)")
	}
	ids := map[string]bool{}
	for _, r := range t.Rows {
		if !config.ID(r.ID) || ids[r.ID] || len(r.Label) > 256 {
			return errors.New("invalid or duplicate monitor row ID/label")
		}
		ids[r.ID] = true
		m, ok := metric(r.Metric)
		if !ok || !slices.Contains(m.Sources, source) {
			return fmt.Errorf("row %s: metric %s unsupported by %s", r.ID, r.Metric, source)
		}
		if r.Visual != "text" && r.Visual != "bar" {
			return fmt.Errorf("row %s: visual must be text or bar", r.ID)
		}
		if r.Visual == "bar" && m.Unit != "percent" {
			return fmt.Errorf("row %s: bar requires a percent metric", r.ID)
		}
		if r.Format != "" && r.Format != m.Unit {
			return fmt.Errorf("row %s: incompatible format", r.ID)
		}
		if r.Select.Check != "" && (!config.ID(r.Select.Check) || m.Selector != "check") {
			return fmt.Errorf("row %s: invalid check selector", r.ID)
		}
		if r.Select.Collector != "" && (len(r.Select.Collector) != 16 || m.Selector != "collector") {
			return fmt.Errorf("row %s: invalid collector selector", r.ID)
		}
	}
	if !ids[t.Bar.Row] {
		return errors.New("bar.row must reference a template row")
	}
	return nil
}
