package tui

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestRenderedRowsFollowEvidenceAndFiltersWithUnchangedRevision(t *testing.T) {
	now := time.Now().UTC()
	m := &Model{Now: now, Width: 80, Height: 24, Tab: tabOverview, Snapshot: model.Snapshot{
		GeneratedAt: now, Revision: "same topology",
		Topology: model.Config{Nodes: []model.Node{{ID: "host", Name: "Host"}}},
		Nodes:    []model.EntityState{{ID: "host", Health: "down"}},
	}}
	m.Render()
	if rows := m.Rows(); len(rows) != 1 || rows[0].ID != "host" {
		t.Fatal("failure not shown", rows)
	}
	m.Snapshot.Nodes[0].Health = "up"
	m.Render()
	if rows := m.Rows(); len(rows) != 0 {
		t.Fatal("render retained an old failure", rows)
	}
	m.Snapshot.Nodes[0].Health = "down"
	m.Search = "does not match"
	m.Render()
	if rows := m.Rows(); len(rows) != 0 {
		t.Fatal("render retained an old filter", rows)
	}
	m.Search = ""
	m.Now = now.Add(time.Minute)
	m.Render()
	for _, row := range m.Rows() {
		if row.Health == "down" {
			t.Fatal("expired evidence remained current", row)
		}
	}
}
