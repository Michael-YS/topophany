package view

import (
	"fmt"
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
