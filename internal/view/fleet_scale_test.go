package view

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func BenchmarkFleetCardsWithServices(b *testing.B) {
	for _, n := range []int{128, 512, 1200} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			v := definitionRowsFixture(n)
			v.Snapshot.Topology.Schema = 1
			v.Snapshot.Topology.Project = "audit"
			v.Snapshot.Topology.Paths = nil
			v.Snapshot.Topology.Observers = nil
			if err := config.Validate(v.Snapshot.Topology); err != nil {
				b.Fatal(err)
			}
			f := v.Fleet()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, node := range v.Snapshot.Topology.Nodes {
					f.Card(node.ID)
				}
			}
		})
	}
}

func TestFleetServiceViaPreservesTopologyOrder(t *testing.T) {
	c := model.Config{
		Nodes:     []model.Node{{ID: "host"}, {ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"}},
		Endpoints: []model.Endpoint{{ID: "first", Node: "host", Via: []string{"b", "a"}}, {ID: "second", Node: "host", Via: []string{"a", "c"}}},
		Services:  []model.Service{{ID: "svc", Node: "host", Endpoints: []string{"second", "first", "second", "missing"}}, {ID: "empty", Node: "host"}},
	}
	fleet := (View{Snapshot: model.Snapshot{Topology: c}}).Fleet()
	card := fleet.Card("host")
	if len(card.Services) != 2 {
		t.Fatal(card.Services)
	}
	want := []Ref{{ID: "b", Name: "B"}, {ID: "a", Name: "A"}, {ID: "c", Name: "C"}}
	if !reflect.DeepEqual(card.Services[0].Via, want) || len(card.Services[1].Via) != 0 {
		t.Fatal(card.Services)
	}
	card.Services[0].Via[0].ID = "changed"
	if !reflect.DeepEqual(fleet.Card("host").Services[0].Via, want) {
		t.Fatal("returned card mutated fleet index")
	}
}
