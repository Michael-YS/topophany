package beszel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// benchmarkIO omits network latency and avoids accumulating request logs.
// Each runtime collection owns a fresh instance, just as the production
// runtime creates a new bounded IO for each scheduled collection.
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

func benchmarkPayload(b testing.TB, disks int, ancillary bool) []byte {
	b.Helper()
	stats := map[string]any{
		"cpu": 17.5,
		"mem": []uint64{17179869184, 4294967296, 1073741824, 0},
		"mp":  25,
		"d":   500,
		"du":  100,
		"dp":  22.5,
	}
	if disks != 0 {
		efs := make(map[string]any, disks)
		for i := 0; i < disks; i++ {
			efs[fmt.Sprintf("volume-%03d", i)] = map[string]any{"d": 1024, "du": 256, "r": 0, "w": 0}
		}
		stats["efs"] = efs
	}
	if ancillary {
		// Beszel returns these real stats fields even though Topophany does
		// not map them. Their representation cost remains part of collection.
		cores := make([]int, 256)
		for i := range cores {
			cores[i] = i % 101
		}
		temperatures := make(map[string]float64, 128)
		for i := 0; i < 128; i++ {
			temperatures[fmt.Sprintf("sensor-%03d", i)] = 40.5
		}
		network := make(map[string][4]uint64, 64)
		for i := 0; i < 64; i++ {
			network[fmt.Sprintf("eth%d", i)] = [4]uint64{1000000, 2000000, 10000000000, 20000000000}
		}
		stats["cpus"], stats["t"], stats["ni"] = cores, temperatures, network
	}
	body, err := json.Marshal(map[string]any{
		"page": 1, "perPage": 1, "totalItems": -1, "totalPages": -1,
		"items": []any{map[string]any{
			"id": testRecord, "system": testSystem, "type": "1m",
			"created": testCreated, "stats": stats,
		}},
	})
	if err != nil {
		b.Fatal(err)
	}
	return body
}

// Measure full adapter validation, request construction, JSON bounds and
// decoding, plus runtime normalization and result ownership. Results describe
// this machine; no wall-clock or allocation threshold is a portable contract.
func BenchmarkBeszelRuntimeCollect(b *testing.B) {
	for _, tc := range []struct {
		name      string
		disks     int
		ancillary bool
	}{
		{"primary-disk", 0, false},
		{"16-extra-disks", 16, false},
		{"48-disks-and-ancillary-stats", 48, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			body := benchmarkPayload(b, tc.disks, tc.ancillary)
			s := testSource()
			s.MaxPages = 1
			runtime := source.Runtime{
				Adapters: map[string]source.Adapter{"beszel": Adapter{}},
				NewIO: func(model.Config, model.Source) (source.IO, error) {
					return &benchmarkIO{body: body}, nil
				},
			}
			request := []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}}
			ctx := context.Background()
			// Validate the workload before timing it. An early parser refusal
			// must never masquerade as fast collection of a large response.
			readings, err := runtime.Collect(ctx, model.Config{}, s, request)
			if err != nil || len(readings) != 1 || readings[0].Report.Resource == nil {
				b.Fatalf("benchmark workload was not admitted: %v", err)
			}
			resource := readings[0].Report.Resource
			if len(resource.Disks) != tc.disks+1 || resource.Memory.TotalBytes == nil || *resource.Memory.TotalBytes != 17179869184 || resource.Memory.UsedBytes == nil || *resource.Memory.UsedBytes != 4294967296 || resource.Disks[0].Percent == nil || *resource.Disks[0].Percent != 22.5 {
				b.Fatal("benchmark workload lost values or disks")
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := runtime.Collect(ctx, model.Config{}, s, request); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
