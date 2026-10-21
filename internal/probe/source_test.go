package probe

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestProductionRegistryDoesNotContainSyntheticAdapters(t *testing.T) {
	c := testkit.Config()
	c.Sources = []model.Source{{ID: "probe-api", Adapter: "synthetic-list", Observer: "home", Target: "https://probe.invalid/v1/objects", IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 10, MaxReportBytes: 16 << 10}}
	c.Checks = append(c.Checks, model.Check{ID: "status", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "svc", Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 60}})
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	if err := NewSourceRuntime(ProductionSourceAdapters()).Validate(c); err == nil {
		t.Fatal("production registry accepted a synthetic source")
	}
}
