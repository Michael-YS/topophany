package monitor

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
	"time"
)

func TestWorkerLocalExternalEvidence(t *testing.T) {
	obj := "aaaaaaaaaaaaaaa"
	p := direct.Profile{Profile: "audit", Project: "audit", Sources: []direct.Entry{{Source: direct.SourceConfig{ID: "hub", Name: "Hub", Adapter: "beszel", URL: "https://example.invalid", Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}, Selected: []string{obj}, Nodes: []direct.Node{{ID: "hub." + obj, ObjectID: obj, Name: "Node"}}}}}
	c, e := p.Config()
	if e != nil {
		t.Fatal(e)
	}
	x := c.Checks[0]
	now := time.Now().UTC()
	cpu := 25.0
	v := model.Observation{ID: "direct.boot.1", Project: c.Project, Observer: x.Observer, Check: x.ID, Contract: config.Contract(c, x), Boot: "boot", Sequence: 1, SampledAt: now, Outcome: "ok", Evidence: "reported", Stage: "external_source", PathEvidence: "external_source", External: &model.ExternalObservation{Source: "hub", SourceGeneration: "audit", AttemptedAt: now, Contact: true, Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: 1, Object: obj, SampleID: "sssssssssssssss", ObservedAt: now, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu}}}}}
	local := NewLocal(c, x.Observer)
	local.Record(v)
	snap := local.Snapshot(now)
	if len(snap.External) != 1 || snap.Checks[0].Freshness != "fresh" {
		t.Fatalf("worker lost valid external reading: external=%d check=%+v", len(snap.External), snap.Checks[0])
	}
	stale := local.Snapshot(now.Add(181 * time.Second))
	if stale.External[0].Freshness != "stale" {
		t.Fatal("local publication renewed upstream data")
	}
	local.Record(v)
	if got := local.external.Bindings[x.ID]; got.ReportSamples != 1 || got.Duplicates != 1 {
		t.Fatal("duplicate counted as new", got)
	}
	c.Checks[0].External.MaxAgeSeconds++
	local.Reload(c)
	if got := local.Snapshot(now); got.Checks[0].Freshness != "never" {
		t.Fatal("changed contract kept evidence")
	}
}
