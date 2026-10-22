package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func externalHeartbeatFixture(t testing.TB) (*Prepared, map[string]model.Heartbeat) {
	t.Helper()
	c := externalEngineConfig(t, model.ReportTypeResource, nil)
	c.Checks = c.Checks[:len(c.Checks)-1]
	c.Sources[0].MaxObjects = model.MaxExternalChecks
	for i := 0; i < model.MaxExternalChecks; i++ {
		node := fmt.Sprintf("resource-node-%03d", i)
		c.Nodes = append(c.Nodes, model.Node{ID: node})
		c.Checks = append(c.Checks, model.Check{
			ID: fmt.Sprintf("resource-%03d", i), Observer: "home", Kind: "external",
			External: &model.ExternalBinding{Source: "probe-api", Object: node, Report: model.ReportTypeResource, Node: node, MaxAgeSeconds: 300, PrimaryDisk: "root"},
		})
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	external := model.NewExternalState()
	external.Sources["probe-api"] = model.SourceState{ID: "probe-api", LastContact: now, LastAttemptAt: now, LastResult: "ok"}
	for _, x := range c.Checks {
		if x.Kind != "external" {
			continue
		}
		report := resourceReport(now, 25)
		report.Object, report.SampleID = x.External.Object, "sample-"+x.ID
		state := model.BindingState{Check: x.ID, Contract: config.Contract(c, x), Source: "probe-api", Object: x.External.Object, ReportType: model.ReportTypeResource, MaxAgeSeconds: 300, Last: &report, DataDeadline: now.Add(300 * time.Second)}
		for i := 0; i < 64; i++ {
			state.Seen = append(state.Seen, model.SeenSample{ID: fmt.Sprintf("%s-%03d", x.ID, i), Hash: strings.Repeat("f", 64)})
		}
		external.Bindings[x.ID] = state
	}
	beats := map[string]model.Heartbeat{"home": {Project: c.Project, Observer: "home", SampledAt: now}}
	return NewPlan(c).PrepareExternal(nil, nil, beats, external), beats
}

var externalHeartbeatPreparedSink *Prepared

func TestExternalHeartbeatRefreshAllocationBudget(t *testing.T) {
	prepared, beats := externalHeartbeatFixture(t)
	allocs := testing.AllocsPerRun(10, func() {
		externalHeartbeatPreparedSink = prepared.WithHeartbeats(beats)
	})
	if allocs > 16 {
		t.Fatalf("heartbeat refresh allocated %.0f objects for unchanged external evidence", allocs)
	}
	if got := externalHeartbeatPreparedSink; got.external == nil || len(got.external.Bindings) != model.MaxExternalChecks {
		t.Fatal("heartbeat refresh dropped prepared external evidence")
	}
}

func BenchmarkExternalHeartbeatRefresh(b *testing.B) {
	prepared, beats := externalHeartbeatFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		externalHeartbeatPreparedSink = prepared.WithHeartbeats(beats)
	}
}
