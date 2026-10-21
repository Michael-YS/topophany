package runner

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func BenchmarkSplitPrepared100(t *testing.B) {
	obs := make([]model.Observation, 100)
	p := 25.0
	for i := range obs {
		obs[i] = model.Observation{ID: fmt.Sprintf("obs.boot.%d", i+1), Check: fmt.Sprintf("check-%03d", i), SampledAt: time.Now(), External: &model.ExternalObservation{Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm"}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &p}}}}}
	}
	t.ResetTimer()
	t.ReportAllocs()
	for i := 0; i < t.N; i++ {
		batches, err := splitPrepared(obs, 512<<10)
		if err != nil || len(batches) != 1 {
			t.Fatal(err, len(batches))
		}
	}
}
