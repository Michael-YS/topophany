package view

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestUnknownActiveChecksPreventAllClear(t *testing.T) {
	for _, freshness := range []string{"fresh", "stale", "never", "paused", "maintenance"} {
		t.Run(freshness, func(t *testing.T) {
			v := View{Snapshot: model.Snapshot{Checks: []model.CheckState{
				{ID: "up", Health: "up", Freshness: "fresh"},
				{ID: "unknown", Health: "unknown", Freshness: freshness},
			}}}
			active := freshness != "paused" && freshness != "maintenance"
			if (v.Verdict().State == "incomplete") != active {
				t.Fatal(v.Verdict())
			}
			rows := v.List(Overview, "")
			if (len(rows) == 1 && rows[0].ID == "unknown") != active {
				t.Fatal(rows)
			}
			if n, _ := v.Badge(Checks); (n == 1) != active {
				t.Fatal(n)
			}
			state, _, _ := v.Calm()
			if (state == "incomplete") != active {
				t.Fatal(state)
			}
		})
	}
}
