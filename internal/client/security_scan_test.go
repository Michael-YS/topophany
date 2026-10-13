package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestSnapshotRejectsUntrustedStructure(t *testing.T) {
	for _, mutate := range []func(*model.Snapshot){
		func(s *model.Snapshot) {
			for i := 0; i < 4097; i++ {
				s.Nodes = append(s.Nodes, model.EntityState{})
			}
		},
		func(s *model.Snapshot) { s.Topology.Nodes[0].Parent = s.Topology.Nodes[0].ID },
		func(s *model.Snapshot) { s.Nodes = []model.EntityState{{ID: "foreign"}} },
		func(s *model.Snapshot) {
			s.Checks = []model.CheckState{{ID: s.Topology.Checks[0].ID, Observer: "foreign"}}
		},
		func(s *model.Snapshot) { s.Topology.Nodes[0].Name = strings.Repeat("x", 4097) },
		func(s *model.Snapshot) { s.Incidents = []model.Incident{{ID: "one", Checks: []string{"foreign"}}} },
	} {
		s := snapshot()
		mutate(&s)
		server := jsonServer(t, 200, s)
		c := newClient(t, server.URL)
		if _, _, err := c.Snapshot(context.Background(), ""); err == nil {
			t.Fatal("unsafe snapshot accepted")
		}
	}
}

func BenchmarkRejectCompactSnapshot(b *testing.B) {
	raw := []byte(`{"nodes":[` + strings.Repeat(`{},`, 100000) + `{}]}`)
	b.ReportAllocs()
	for b.Loop() {
		var s model.Snapshot
		if err := decodeSnapshot(raw, &s); err == nil {
			b.Fatal("accepted")
		}
	}
}

func FuzzSnapshotStructure(f *testing.F) {
	raw, _ := json.Marshal(snapshot())
	f.Add(raw)
	f.Add([]byte(fmt.Sprintf(`{"nodes":[%s{}]}`, strings.Repeat(`{},`, 4096))))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > model.MaxSnapshotBytes {
			return
		}
		var s model.Snapshot
		_ = decodeSnapshot(raw, &s)
	})
}
