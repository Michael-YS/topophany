package probe

import (
	"context"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestProbeDurationSurvivesWallClockSteps(t *testing.T) {
	for _, shift := range []time.Duration{-time.Second, time.Hour} {
		t.Run(shift.String(), func(t *testing.T) {
			const elapsed = 100 * time.Millisecond
			x := model.Check{ID: "host", Observer: "home", Kind: "host", TimeoutSeconds: 8}
			calls := 0
			r := Runner{
				HostSample: func(context.Context, model.HostOptions) (HostSample, error) {
					return hostFixture(), nil
				},
				since: func(start time.Time) time.Duration {
					calls++
					// Round(0) strips the monotonic reading. Model time.Since's
					// fallback to wall time when the caller has already lost it.
					if start == start.Round(0) {
						return elapsed + shift
					}
					return elapsed
				},
			}
			o := r.Run(context.Background(), testkit.Config(), x)
			if calls != 1 || o.DurationMS != elapsed.Milliseconds() || o.Outcome != "ok" {
				t.Fatalf("wall-clock step %s: calls=%d, observation=%+v", shift, calls, o)
			}
			if o.SampledAt.IsZero() || o.SampledAt.Location() != time.UTC {
				t.Fatalf("sample timestamp must remain UTC: %v", o.SampledAt)
			}
			if code := config.ObservationBounds(o, time.Duration(x.TimeoutSeconds+5)*time.Second); code != "" {
				t.Fatalf("valid probe rejected after clock step: %s", code)
			}
		})
	}
}
