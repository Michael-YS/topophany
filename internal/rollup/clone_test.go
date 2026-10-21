package rollup

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCloneOwnsMutablePersistenceState(t *testing.T) {
	r := New(MinLimit)
	edgeTail(r, "edge", t0)
	copy := r.Clone()
	before, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	r.Add("edge", "", t0.Add(-30*time.Second), "ok", 900, time.Minute, t0, Extra{FirstByteMS: 80, HasFirstByte: true, HasEdge: true, Cache: "HIT", Pop: "LAX"})
	r.Settle(t0.Add(time.Hour))
	after, err := json.Marshal(copy)
	if err != nil || string(before) != string(after) {
		t.Fatal("live ingestion mutated captured rollups", err)
	}
	live, _ := json.Marshal(r)
	if string(live) == string(after) {
		t.Fatal("fixture failed to mutate live rollups")
	}
}
