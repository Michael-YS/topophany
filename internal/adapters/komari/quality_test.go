package komari

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

type benchmarkIO struct {
	body  []byte
	pages int
}

func (io *benchmarkIO) Credential(context.Context, model.Source) (string, error) {
	return testToken, nil
}
func (io *benchmarkIO) Get(context.Context, model.Source, string, map[string]string, int64) (source.Response, error) {
	io.pages++
	return source.Response{Status: http.StatusOK, ContentType: "application/json", Body: io.body}, nil
}
func (io *benchmarkIO) Pages() int { return io.pages }

// Measure the adapter plus production normalization. One minute at the
// official default cadence is roughly 60 records; 600 stresses faster agents.
// Network latency is omitted and no machine-specific timing is a test gate.
func BenchmarkKomariRuntimeCollect(b *testing.B) {
	for _, count := range []int{1, 60, 600} {
		b.Run(fmt.Sprintf("%d-reports", count), func(b *testing.B) {
			at := time.Date(2026, 10, 22, 12, 34, 56, 0, time.UTC)
			records := make([]string, count)
			for i := range records {
				records[i] = sample(testObject, at.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano), `"cpu":{"usage":12.5,"cores":4},"ram":{"total":17179869184,"used":4294967296},"disk":{"total":107374182400,"used":21474836480},"network":{"up":1234,"down":5678,"totalUp":34567890123,"totalDown":98765432101},"load":{"load1":0.5,"load5":0.75,"load15":1},"swap":{"total":2147483648,"used":0},"connections":{"tcp":12,"udp":3},"uptime":86400,"process":73,"message":""`)
			}
			body := recent(records...)
			s := testSource()
			s.MaxPages = 1
			runtime := source.Runtime{Adapters: map[string]source.Adapter{"komari": Adapter{}}, NewIO: func(model.Config, model.Source) (source.IO, error) { return &benchmarkIO{body: body}, nil }}
			ctx := context.Background()
			requests := requestsFor(testObject)
			got, err := runtime.Collect(ctx, model.Config{}, s, requests)
			if err != nil || len(got) != 1 || got[0].Report.Resource == nil {
				b.Fatalf("benchmark workload was not actually collected: %v", err)
			}
			r := got[0].Report
			if !r.ObservedAt.Equal(at.Add(time.Duration(count-1) * time.Millisecond)) {
				b.Fatal("benchmark did not select the latest sample")
			}
			wantValue(b, "cpu", r.Resource.CPU.Percent, 12.5)
			wantValue(b, "memory", r.Resource.Memory.Percent, 25.0)
			wantValue(b, "disk", r.Resource.Disks[0].Percent, 20.0)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := runtime.Collect(ctx, model.Config{}, s, requests); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestKomariRuntimeEnforcesNormalizedReportBudget(t *testing.T) {
	s := testSource()
	s.MaxReportBytes = 128
	runtime := source.Runtime{Adapters: map[string]source.Adapter{"komari": Adapter{}}, NewIO: func(model.Config, model.Source) (source.IO, error) { return &benchmarkIO{body: readFixture(t)}, nil }}
	got, err := runtime.Collect(context.Background(), model.Config{}, s, requestsFor(testObject))
	if source.Code(err) != "report_too_large" || len(got) != 0 {
		t.Fatalf("normalized report budget was bypassed: reports=%d error=%v", len(got), err)
	}
}

func (*benchmarkIO) Close() {}
