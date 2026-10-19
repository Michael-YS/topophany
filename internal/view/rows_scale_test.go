package view

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func endpointRowsFixture(n int) View {
	v := View{Now: time.Now()}
	for i := 0; i < n; i++ {
		id, via := fmt.Sprint("node-", i), fmt.Sprint("node-", (i+1)%n)
		v.Snapshot.Topology.Nodes = append(v.Snapshot.Topology.Nodes, model.Node{ID: id, Name: id, Kind: "server"})
		v.Snapshot.Topology.Endpoints = append(v.Snapshot.Topology.Endpoints, model.Endpoint{ID: fmt.Sprint("ep-", i), Node: id, Via: []string{via}, Network: "public", Address: "192.0.2.1"})
	}
	return v
}

func definitionRowsFixture(n int) View {
	v := endpointRowsFixture(n)
	for i := 0; i < n; i++ {
		id := fmt.Sprint("item-", i)
		node := fmt.Sprint("node-", i)
		v.Snapshot.Topology.Services = append(v.Snapshot.Topology.Services, model.Service{ID: id, Name: "service " + id, Node: node, Endpoints: []string{fmt.Sprint("ep-", i)}})
		v.Snapshot.Topology.Paths = append(v.Snapshot.Topology.Paths, model.Path{ID: id, Name: "path " + id, Observer: id, Nodes: []string{node}})
		v.Snapshot.Topology.Observers = append(v.Snapshot.Topology.Observers, model.Observer{ID: id, Name: "observer " + id, Node: node})
		health := "up"
		if i%32 == 0 {
			health = "down"
		}
		v.Snapshot.Services = append(v.Snapshot.Services, model.EntityState{ID: id, Health: health})
		v.Snapshot.Paths = append(v.Snapshot.Paths, model.EntityState{ID: id, Health: health})
		v.Snapshot.Observers = append(v.Snapshot.Observers, model.ObserverState{ID: id, Health: health})
		v.Snapshot.Nodes = append(v.Snapshot.Nodes, model.EntityState{ID: node, Health: health})
	}
	return v
}

func TestIndexedDefinitionsAndOverviewMatchFullRows(t *testing.T) {
	v := definitionRowsFixture(256)
	for _, tab := range []int{Services, Paths, Observers} {
		rows := v.Rows(tab)
		for i, row := range rows {
			if row.Title != map[int]string{Services: "service", Paths: "path", Observers: "observer"}[tab]+" "+fmt.Sprint("item-", i) {
				t.Fatal("definition did not match state", tab, row.ID, row.Title)
			}
		}
	}
	want := []Row{}
	for _, tab := range []int{Services, Paths, Topology, Observers} {
		for _, r := range v.Rows(tab) {
			if (tab != Topology || r.Kind == "node") && NeedsAttention(r) {
				r.Tree, r.Cols = "", []string{r.Kind}
				want = append(want, r)
			}
		}
	}
	sort.SliceStable(want, func(i, j int) bool { return AttentionRank(want[i]) < AttentionRank(want[j]) })
	if got := v.Rows(Overview); !reflect.DeepEqual(got, want) {
		t.Fatal("attention-only derivation differs from full tabs", len(got), len(want))
	}
}

func BenchmarkDefinitionRows(b *testing.B) {
	for _, n := range []int{256, 1024, 4096} {
		v := definitionRowsFixture(n)
		for _, tab := range []int{Services, Paths, Observers, Overview} {
			b.Run(fmt.Sprintf("%d/%s", n, Tabs[tab]), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					v.Rows(tab)
				}
			})
		}
	}
}

func TestEndpointIndexesPreserveOwnershipAndReverseVia(t *testing.T) {
	v := endpointRowsFixture(32)
	for _, tab := range []int{Topology, Servers, Overview} {
		for _, r := range v.Rows(tab) {
			if r.Kind != "node" {
				continue
			}
			var i int
			fmt.Sscanf(r.ID, "node-%d", &i)
			details := strings.Join(r.Details, "\n")
			own := fmt.Sprintf("ep-%d [public]", i)
			front := fmt.Sprintf("ep-%d → node-%d", (i+31)%32, (i+31)%32)
			if !strings.Contains(details, own) || !strings.Contains(details, front) {
				t.Fatal("ownership or reverse Via lost", r.ID, details)
			}
		}
	}
}

func BenchmarkEndpointRows(b *testing.B) {
	for _, n := range []int{256, 1024, 4096} {
		v := endpointRowsFixture(n)
		for _, tab := range []int{Topology, Servers} {
			b.Run(fmt.Sprintf("%d/%s", n, Tabs[tab]), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					v.Rows(tab)
				}
			})
		}
	}
}
