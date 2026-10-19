package view

import (
	"fmt"
	"slices"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestGraphInheritsLocationAcrossEntireParentChain(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		c := model.Config{Schema: 1, Project: "placement"}
		for i := range 32 {
			n := model.Node{ID: fmt.Sprint("node-", i)}
			if i == 0 {
				n.Location = &model.Location{Lat: 30, Lon: 120, Label: "root site"}
			} else {
				n.Parent = fmt.Sprint("node-", i-1)
			}
			c.Nodes = append(c.Nodes, n)
		}
		if reverse {
			slices.Reverse(c.Nodes)
		}
		if err := config.Validate(c); err != nil {
			t.Fatal(err)
		}
		nodes, _ := (View{Snapshot: model.Snapshot{Topology: c}}).Graph()
		for _, n := range nodes {
			if !n.Located || n.Lat != 30 || n.Lon != 120 || n.Place != "root site" || n.Inherited != (n.ID != "node-0") {
				t.Fatalf("reverse=%v: %+v", reverse, n)
			}
		}
	}
}

func TestGraphPlacementHandlesDraftCyclesAndExplicitChildSites(t *testing.T) {
	c := model.Config{Nodes: []model.Node{
		{ID: "a", Parent: "b"}, {ID: "b", Parent: "a"},
		{ID: "missing", Parent: "absent"},
		{ID: "child", Parent: "parent", Location: &model.Location{Lat: 2}},
		{ID: "parent", Location: &model.Location{Lat: 1}},
	}}
	nodes, _ := (View{Snapshot: model.Snapshot{Topology: c}, Draft: true}).Graph()
	for _, n := range nodes {
		if n.ID == "a" || n.ID == "b" || n.ID == "missing" {
			if n.Located {
				t.Fatal("unplaced draft inherited a location", n)
			}
		} else if !n.Located || n.Inherited || n.ID == "child" && n.Lat != 2 {
			t.Fatal("explicit placement changed", n)
		}
	}
}
