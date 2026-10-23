package store

import (
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
	"time"
)

func TestCollectorFleetReachesReplayWindow(t *testing.T) {
	c := model.Config{Schema: model.ConfigSchema, Project: "audit", Observers: []model.Observer{{ID: "home", Name: "Home", Context: "local", StaleSeconds: 180}}}
	for i := 0; i < 128; i++ {
		id := fmt.Sprintf("n%d", i)
		src := fmt.Sprintf("s%d", i/20)
		if i%20 == 0 {
			c.Sources = append(c.Sources, model.Source{ID: src, Name: src, Adapter: "komari", Observer: "home", Target: "https://example.invalid", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, MaxPages: 20})
		}
		obj := fmt.Sprintf("%08x-0000-0000-0000-%012x", i, i)
		c.Nodes = append(c.Nodes, model.Node{ID: id, Name: id, Kind: "host", ResourceChecks: []string{id}})
		c.Checks = append(c.Checks, model.Check{ID: id, Name: id, Kind: "external", Observer: "home", External: &model.ExternalBinding{Source: src, Object: obj, Report: model.ReportTypeResource, Node: id, MaxAgeSeconds: 180, PrimaryDisk: "aggregate"}})
	}
	config.Defaults(&c)
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s, e := Open(t.TempDir(), Options{Now: func() time.Time { return now }, MaxHistory: 1, CheckpointBytes: 128 << 20, MaxJournalBytes: 128 << 20}.WithTopology(c))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	contracts := config.Contracts(c)
	revision := config.Revision(c)
	var seq uint64
	for round := 0; round < 257; round++ {
		now = now.Add(time.Minute)
		for _, src := range c.Sources {
			b := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: revision}
			for _, x := range c.Checks {
				if x.External.Source != src.ID {
					continue
				}
				seq++
				n := 25.0
				r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: 1, Object: x.External.Object, SampleID: fmt.Sprintf("komari-%064d", round), ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &n}}}
				b.Observations = append(b.Observations, model.Observation{ID: fmt.Sprintf("home.boot.%d", seq), Project: c.Project, Observer: "home", Boot: "boot", Sequence: seq, Check: x.ID, Contract: contracts[x.ID], SampledAt: now, Outcome: "ok", Evidence: "reported", Stage: "external_source", PathEvidence: "external_source", External: &model.ExternalObservation{Source: src.ID, SourceGeneration: "audit", AttemptedAt: now, Contact: true, Report: &r}})
			}
			if e := s.Put(b); e != nil {
				t.Logf("accepted external state=%d bytes; failure round=%d source=%s error=%v", s.externalBytes, round+1, src.ID, e)
				now = now.Add(48 * time.Hour)
				for i := range b.Observations {
					v := &b.Observations[i]
					v.SampledAt = now
					v.External.AttemptedAt = now
					v.External.Report.ObservedAt = now
				}
				again := s.Put(b)
				t.Fatalf("valid fleet cannot reach 256-entry window: first=%v after48h=%v", e, again)
			}
		}
	}
}
