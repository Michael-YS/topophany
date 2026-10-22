package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
)

func TestBeszelPreciseMemorySurvivesCollectorHTTPAndMonitorWire(t *testing.T) {
	const total, used = uint64(9007199254740995), uint64(9007199254740993)
	hub := newBeszelHubFixture(t)
	hub.setSample(beszelRecordID, hub.created, `{"cpu":0,"mem":[9007199254740995,9007199254740993,0,0],"mp":42.25,"d":100,"du":20,"dp":23.25}`)
	ch := newBeszelChain(t, beszelIntegrationConfig(t, hub.server.URL))
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	if err := ch.st.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	reader, err := client.New(client.Profile{Project: ch.cfg.Project, Servers: []client.Endpoint{{URL: ch.collector.URL, Token: viewerToken}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	// Read and validate the public snapshot over HTTP, rather than merely
	// inspecting the adapter's in-memory result before JSON serialization.
	snapshot, endpoint, err := reader.Snapshot(ctx, "")
	if err != nil || endpoint != ch.collector.URL {
		t.Fatalf("collector snapshot read failed: %v", err)
	}
	seen := 0
	for _, binding := range snapshot.External {
		if binding.ReportType != model.ReportTypeResource {
			continue
		}
		seen++
		if binding.Report == nil || binding.Report.Resource == nil || binding.Report.Resource.Memory == nil {
			t.Fatalf("resource omitted from collector HTTP snapshot: %+v", binding)
		}
		memory := binding.Report.Resource.Memory
		if memory.TotalBytes == nil || *memory.TotalBytes != total || memory.UsedBytes == nil || *memory.UsedBytes != used || memory.Percent == nil || *memory.Percent != 42.25 || memory.AvailableBytes != nil || memory.Basis != "beszel_bytes" {
			t.Fatalf("precise memory changed across collector HTTP: %+v", memory)
		}
	}
	if seen != 2 {
		t.Fatalf("resource bindings received over HTTP = %d, want 2", seen)
	}
	cfg := monitor.Config{Name: "precise-memory", Project: snapshot.Project, Source: "preview", Template: monitor.Template{Schema: 1, Title: "Topophany", Rows: []monitor.Row{{ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}}}}}
	panel := monitor.Build(cfg, monitor.Input{Snapshot: snapshot}, snapshot.GeneratedAt)
	panel.PublisherExpiresAt = snapshot.GeneratedAt.Add(30 * time.Second)
	var wire strings.Builder
	if err := stream.WriteFrame(&wire, panel); err != nil {
		t.Fatal(err)
	}
	// Decode the wire as typed uint64 fields, not a map[string]any that would
	// itself round JSON integers to float64 and invalidate the test oracle.
	var frame struct {
		Rows []struct {
			Value monitor.ServerCard `json:"value"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(wire.String()), &frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.Rows) != 1 || len(frame.Rows[0].Value.Resources) != 1 {
		t.Fatalf("monitor resource was omitted: %+v", frame)
	}
	resource := frame.Rows[0].Value.Resources[0]
	if resource.Memory.Total == nil || *resource.Memory.Total != total || resource.Memory.Used == nil || *resource.Memory.Used != used || resource.Memory.Percent == nil || *resource.Memory.Percent != 42.25 || resource.Memory.Basis != "beszel_bytes" {
		t.Fatalf("monitor wire rounded integer memory or replaced mp: %+v", resource.Memory)
	}
	if resource.SampledAt == nil || !resource.SampledAt.Equal(hub.created) || !resource.DataDeadline.Equal(hub.created.Add(180*time.Second)) {
		t.Fatalf("exact data lost its upstream evidence time: %+v", resource)
	}
	beszelAssertNoSecrets(t, snapshot, panel, frame)
}

func TestBeszelNormalizedReportBudgetFailsProductionCollection(t *testing.T) {
	hub := newBeszelHubFixture(t)
	disks := make(map[string]any, 20)
	for i := 0; i < 20; i++ {
		disks[fmt.Sprintf("volume-%02d", i)] = map[string]any{"d": 100, "du": 25}
	}
	stats, err := json.Marshal(map[string]any{"cpu": 10, "m": 8, "mu": 2, "mp": 25, "efs": disks})
	if err != nil {
		t.Fatal(err)
	}
	hub.setSample(beszelRecordID, hub.created, string(stats))
	c := beszelIntegrationConfig(t, hub.server.URL)
	c.Sources[0].MaxReportBytes = 1024
	ch := newBeszelChain(t, c)
	ctx := context.Background()
	ch.once(ctx)
	ch.deliver(ctx)
	// Both API reads succeed; the normalized resource itself exceeds the
	// reviewed output budget. No partial resource or untimed status may be
	// delivered as a successful source collection after that refusal.
	if hub.count(beszelPrefix+"/api/collections/system_stats/records") != 1 || hub.count(beszelPrefix+"/api/collections/systems/records/"+beszelSystemID) != 1 {
		t.Fatal("normalized-report test did not complete both successful API reads")
	}
	state := ch.st.ExternalView()
	source := state.Sources["beszel-hub"]
	if source.LastCode != "report_too_large" || source.LastResult != "error" || !source.LastContact.IsZero() {
		t.Fatalf("normalized report overflow became successful contact: %+v", source)
	}
	if len(ch.batches) == 0 {
		t.Fatal("report overflow produced no diagnostic batch")
	}
	for _, batch := range ch.batches {
		for _, observation := range batch.Observations {
			if observation.Code != "report_too_large" || observation.External == nil || observation.External.Contact || observation.External.Report != nil || observation.Connectivity {
				t.Fatalf("report overflow admitted partial evidence: %+v", observation)
			}
		}
	}
	for name, binding := range state.Bindings {
		if binding.Last != nil || binding.ReportSamples != 0 || binding.ConfirmedDown {
			t.Fatalf("%s accepted evidence from an over-budget collection: %+v", name, binding)
		}
	}
	beszelAssertNoSecrets(t, state, ch.batches)
}
