package direct

import (
	"fmt"
	"testing"
)

func TestUnchangedProfileRetainsPreparedEvidence(t *testing.T) {
	s, _, _ := fleetSession(t, 256, 256)
	p := s.Profile()
	prepared, external, plan := s.prepared, s.external, s.plan
	if n := testing.AllocsPerRun(10, func() {
		if e := s.Reconfigure(p); e != nil {
			t.Fatal(e)
		}
	}); n != 0 {
		t.Fatalf("unchanged profile allocated %.0f objects", n)
	}
	if s.prepared != prepared || s.external != external || s.plan != plan {
		t.Fatal("projection rebuilt accepted evidence")
	}
	// A content edit must still apply even when an embedding caller reuses a revision.
	p.Sources[0].Nodes[0].Name = "renamed"
	if e := s.Reconfigure(p); e != nil {
		t.Fatal(e)
	}
	if s.config.Nodes[0].Name != "renamed" || s.prepared == prepared {
		t.Fatal("metadata edit skipped")
	}
	p.Sources[0].Nodes[0].Name = "caller mutation"
	if s.profile.Sources[0].Nodes[0].Name != "renamed" {
		t.Fatal("profile ownership lost")
	}
	s.closed = true
	if e := s.Reconfigure(s.Profile()); e == nil {
		t.Fatal("closed session accepted request")
	}
}

func BenchmarkProjectionReplacement(b *testing.B) {
	for _, objects := range []int{20, 256} {
		b.Run(fmt.Sprint(objects), func(b *testing.B) {
			s, _, _ := fleetSession(b, objects, 256)
			p := s.Profile()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if e := s.Reconfigure(p); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
