package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// The Observers tab carries one reported line from the observer's newest fresh
// trace check. A trace sample that is stale or absent leaves the row as it
// was, and only the fields the trace reported appear.
func TestObserverRowShowsNewestFreshTrace(t *testing.T) {
	now := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC)
	fixture := func(t *testing.T) (model.Config, model.Observation) {
		t.Helper()
		c := testkit.Config()
		c.Checks[0].HTTP.ExitFormat = "trace"
		return c, testkit.Observation(c, c.Checks[0].ID, 1, now.Add(-time.Second), "ok")
	}
	row := func(v View) string {
		t.Helper()
		for _, r := range v.Rows(Observers) {
			if r.ID == "home" {
				return strings.Join(r.Details, "\n")
			}
		}
		t.Fatal("no home observer row")
		return ""
	}
	for _, tc := range []struct {
		name  string
		facts map[string]string
		want  string
	}{
		{"warp and gateway", map[string]string{"trace_warp": "on", "trace_gateway": "off"}, "Trace: WARP on · Gateway off (reported, not verified)"},
		{"warp only", map[string]string{"trace_warp": "plus"}, "Trace: WARP plus (reported, not verified)"},
		{"gateway only", map[string]string{"trace_gateway": "on"}, "Trace: Gateway on (reported, not verified)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, o := fixture(t)
			for k, v := range tc.facts {
				o.Facts[k] = v
			}
			details := row(View{Snapshot: engine.Build(c, "primary", "", []model.Observation{o}, nil, now), Now: now})
			if !strings.Contains(details, tc.want) {
				t.Fatalf("details do not carry %q:\n%s", tc.want, details)
			}
		})
	}
	t.Run("newest trace check wins", func(t *testing.T) {
		c, older := fixture(t)
		older.SampledAt = now.Add(-2 * time.Second)
		older.Facts["trace_warp"] = "off"
		second := c.Checks[0]
		second.ID = "trace-home-2"
		c.Checks = append(c.Checks, second)
		newer := testkit.Observation(c, second.ID, 1, now.Add(-time.Second), "ok")
		newer.Facts["trace_warp"] = "on"
		details := row(View{Snapshot: engine.Build(c, "primary", "", []model.Observation{older, newer}, nil, now), Now: now})
		if !strings.Contains(details, "Trace: WARP on (reported, not verified)") || strings.Contains(details, "WARP off") {
			t.Fatalf("the newest trace check did not decide the line:\n%s", details)
		}
	})
	t.Run("stale trace sample", func(t *testing.T) {
		c, o := fixture(t)
		o.SampledAt = now.Add(-10 * time.Minute)
		o.Facts["trace_warp"] = "on"
		details := row(View{Snapshot: engine.Build(c, "primary", "", []model.Observation{o}, nil, now), Now: now})
		if strings.Contains(details, "Trace:") {
			t.Fatalf("a stale trace sample produced a line:\n%s", details)
		}
	})
	t.Run("no trace sample", func(t *testing.T) {
		c, _ := fixture(t)
		details := row(View{Snapshot: engine.Build(c, "primary", "", nil, nil, now), Now: now})
		if strings.Contains(details, "Trace:") {
			t.Fatalf("an absent trace sample produced a line:\n%s", details)
		}
	})
	t.Run("a non-trace sample is not read", func(t *testing.T) {
		c := testkit.Config()
		c.Checks[0].HTTP.ExitFormat = ""
		o := testkit.Observation(c, c.Checks[0].ID, 1, now.Add(-time.Second), "ok")
		o.Facts["trace_warp"] = "on"
		details := row(View{Snapshot: engine.Build(c, "primary", "", []model.Observation{o}, nil, now), Now: now})
		if strings.Contains(details, "Trace:") {
			t.Fatalf("a non-trace check produced a line:\n%s", details)
		}
	})
}
