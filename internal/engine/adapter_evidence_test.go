package engine

import (
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestReportedOnlyPathExplainsUnmeasuredReachability(t *testing.T) {
	for _, outcome := range []string{"ok", "policy", "unknown"} {
		state := model.CheckState{ID: "runtime", Health: "up", Freshness: "fresh", Last: &model.Observation{Evidence: "reported", Outcome: outcome}}
		path := aggregatePath("path", []model.CheckState{state}, "empty")
		if path.Health != "unknown" || !strings.Contains(path.Reason, "runtime reports only") {
			t.Fatal(path)
		}
		state.Freshness = "stale"
		if strings.Contains(aggregatePath("path", []model.CheckState{state}, "empty").Reason, "runtime reports only") {
			t.Fatal("stale telemetry presented as current")
		}
	}
	measured := model.CheckState{ID: "https", Health: "down", Freshness: "fresh", Last: &model.Observation{Evidence: "measured", Outcome: "fail"}}
	if state := aggregatePath("path", []model.CheckState{measured}, "empty"); state.Health != "down" {
		t.Fatal(state)
	}
}
