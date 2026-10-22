package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
)

func TestKomariExactBytesSurviveCollectorHTTPAndMonitorWire(t *testing.T) {
	const total, used = uint64(9007199254740995), uint64(9007199254740993)
	h := newKomariFixture(t)
	h.setSample(h.at, `"cpu":{"usage":12.5},"ram":{"total":9007199254740995,"used":9007199254740993},"disk":{"total":9007199254740995,"used":9007199254740993}`)
	ch := newKomariChain(t, komariIntegrationConfig(t, h.server.URL))
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
		if binding.Report == nil || binding.Report.Resource == nil || binding.Report.Resource.Memory == nil || len(binding.Report.Resource.Disks) != 1 {
			t.Fatalf("resource omitted from collector HTTP snapshot: %+v", binding)
		}
		memory := binding.Report.Resource.Memory
		disk := binding.Report.Resource.Disks[0]
		if memory.TotalBytes == nil || *memory.TotalBytes != total || memory.UsedBytes == nil || *memory.UsedBytes != used || disk.TotalBytes == nil || *disk.TotalBytes != total || disk.UsedBytes == nil || *disk.UsedBytes != used {
			t.Fatalf("exact byte counters changed across collector HTTP: memory=%+v disk=%+v", memory, disk)
		}
	}
	if seen != 2 {
		t.Fatalf("resource bindings received over HTTP = %d, want 2", seen)
	}
	cfg := monitor.Config{Name: "precise-komari", Project: snapshot.Project, Source: "preview", Template: monitor.Template{Schema: 1, Title: "Topophany", Rows: []monitor.Row{{ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}}}}}
	panel := monitor.Build(cfg, monitor.Input{Snapshot: snapshot}, snapshot.GeneratedAt)
	panel.PublisherExpiresAt = snapshot.GeneratedAt.Add(30 * time.Second)
	var wire strings.Builder
	if err := stream.WriteFrame(&wire, panel); err != nil {
		t.Fatal(err)
	}
	// Use typed counters to test the consumer's actual integer contract;
	// map[string]any would round these values in the test itself.
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
	r := frame.Rows[0].Value.Resources[0]
	if r.Memory.Total == nil || *r.Memory.Total != total || r.Memory.Used == nil || *r.Memory.Used != used || r.Disk.Total == nil || *r.Disk.Total != total || r.Disk.Used == nil || *r.Disk.Used != used {
		t.Fatalf("monitor wire rounded byte counters: %+v", r)
	}
	if r.SampledAt == nil || !r.SampledAt.Equal(h.at) || !r.DataDeadline.Equal(h.at.Add(180*time.Second)) {
		t.Fatalf("exact counters lost their upstream evidence time: %+v", r)
	}
	komariAssertNoSecrets(t, snapshot, panel, frame)
}

func TestKomariCPUZeroAndMissingRemainDistinctInMonitor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cpu     string
		missing bool
	}{
		// Komari 1.6.0's concrete CPU struct always exists, but usage is
		// omitted at zero. An absent/null CPU or explicit null usage is
		// not that representation and must remain unavailable instead.
		{"upstream-omitempty-zero", `"cpu":{}`, false},
		{"null-cpu", `"cpu":null`, true},
		{"null-usage", `"cpu":{"usage":null}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newKomariFixture(t)
			h.setSample(h.at, strings.Replace(komariStats, `"cpu":{"usage":12.5}`, tc.cpu, 1))
			ch := newKomariChain(t, komariIntegrationConfig(t, h.server.URL))
			ctx := context.Background()
			ch.once(ctx)
			ch.deliver(ctx)
			b := ch.st.ExternalView().Bindings["komari-resource"]
			if b.Last == nil || b.Last.Resource == nil || b.Last.Resource.Memory == nil || b.Last.Resource.Memory.Percent == nil || *b.Last.Resource.Memory.Percent != 37.5 {
				t.Fatalf("CPU availability discarded independent memory: %+v", b.Last)
			}
			var cpu *float64
			if b.Last.Resource.CPU != nil {
				cpu = b.Last.Resource.CPU.Percent
			}
			if tc.missing && (cpu != nil || b.Last.Fields["cpu.percent"] != model.FieldMissing) || !tc.missing && (cpu == nil || *cpu != 0) {
				t.Fatalf("CPU zero/missing contract changed: %+v", b.Last)
			}
			panel, _ := monitorFrame(t, ch, time.Now().UTC().Add(time.Second), []monitor.Row{{ID: "host", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "dmit"}}, {ID: "cpu", Metric: "resource.cpu.used_percent", Visual: "bar", Select: monitor.Selector{Node: "dmit"}}})
			card, ok := panel.Rows[0].Value.(monitor.ServerCard)
			if !ok || len(card.Resources) != 1 || card.Resources[0].Memory.Percent == nil || *card.Resources[0].Memory.Percent != 37.5 {
				t.Fatalf("monitor lost independent memory value: %+v", panel.Rows)
			}
			cpu = card.Resources[0].CPU.Percent
			if tc.missing && (cpu != nil || panel.Rows[1].Value != nil) || !tc.missing && (cpu == nil || *cpu != 0 || panel.Rows[1].Value != float64(0)) {
				t.Fatalf("monitor changed zero/missing CPU semantics: %+v", panel.Rows)
			}
		})
	}
}
