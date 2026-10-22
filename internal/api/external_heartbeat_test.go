package api

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestExternalEvidenceSurvivesHeartbeatRefreshWithoutExtendingDeadlines(t *testing.T) {
	srv, _, batch, c := externalAPIFixture(t)
	if err := srv.Store.Put(batch); err != nil {
		t.Fatal(err)
	}
	now := srv.Now()
	find := func(snap model.Snapshot) model.ExternalBindingView {
		t.Helper()
		for _, binding := range snap.External {
			if binding.Check == "vm-resources" {
				return binding
			}
		}
		t.Fatal("accepted external binding disappeared")
		return model.ExternalBindingView{}
	}
	first := find(srv.MonitorSnapshot(now))
	if first.Report == nil || first.Freshness != "fresh" {
		t.Fatalf("initial evidence = %+v", first)
	}
	evidenceVersion := srv.evaluationEvidenceVersion
	beat := model.Heartbeat{Observer: "home", Boot: "heartbeat", SampledAt: now, Contracts: config.ObserverContracts(c, "home")}
	if err := srv.Store.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Heartbeat: &beat}); err != nil {
		t.Fatal(err)
	}
	// Published reports remain independently mutable across heartbeat refreshes.
	*first.Report.Resource.CPU.Percent = 99
	second := find(srv.MonitorSnapshot(now.Add(time.Second)))
	if second.Report == nil || *second.Report.Resource.CPU.Percent != 25 || second.Freshness != "fresh" || srv.evaluationEvidenceVersion != evidenceVersion {
		t.Fatalf("heartbeat lost or changed accepted evidence: %+v", second)
	}
	if !second.SampledAt.Equal(first.SampledAt) || !second.DataDeadline.Equal(first.DataDeadline) || !second.ContactDeadline.Equal(first.ContactDeadline) {
		t.Fatalf("heartbeat refreshed evidence clocks: first=%+v second=%+v", first, second)
	}
	if expired := find(srv.MonitorSnapshot(first.ContactDeadline)); expired.Freshness != "stale" || expired.Health != "unknown" {
		t.Fatalf("heartbeat kept expired report current: %+v", expired)
	}
}
