package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// /v1/rollups and the snapshot's availability carry the rollup's
// first-byte classes, colo counts and cache counts in the documented JSON
// shape. The fields are additive: a schema 2 reader that predates them
// ignores them and still reads the total.
func TestRollupFirstBytePopsAndCacheJSON(t *testing.T) {
	srv, h, b := fixture(t)
	c := srv.Config
	now := srv.Now()
	b.Observations = nil
	for i, tc := range []struct {
		outcome string
		first   int64
		cache   string
		pop     string
	}{
		{"ok", 40, "HIT", "LAX"},
		{"ok", 600, "MISS", "HKG"},
		{"fail", 900, "HIT", "LAX"},
	} {
		o := testkit.Observation(c, "home-http", uint64(i+1), now.Add(-time.Duration(3-i)*time.Minute), tc.outcome)
		o.DurationMS = 1000
		o.Timings = map[string]int64{"first_byte_ms": tc.first}
		o.Facts["edge_provider"], o.Facts["edge_cache"], o.Facts["edge_pop"] = "cloudflare", tc.cache, tc.pop
		b.Observations = append(b.Observations, o)
	}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request(h, "GET", "/v1/rollups?check=home-http&step=5m&span=1h", viewerToken, "", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var raw struct {
		Total map[string]json.RawMessage `json:"total"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &raw); e != nil {
		t.Fatal(e)
	}
	rollupsBody := w.Body.Bytes()
	for _, key := range []string{"first_byte", "pops", "cache"} {
		if _, ok := raw.Total[key]; !ok {
			t.Fatalf("the total has no %s: %s", key, w.Body.String())
		}
	}
	var got struct {
		Total struct {
			FirstByte *model.FirstByteLatency `json:"first_byte"`
			Pops      map[string]int64        `json:"pops"`
			Cache     map[string]int64        `json:"cache"`
		} `json:"total"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil {
		t.Fatal(e)
	}
	fb, _ := json.Marshal(got.Total.FirstByte)
	wantFB := `{"cached":{"samples":1,"p50_ms":40,"p95_ms":40,"max_ms":40},"origin":{"samples":1,"p50_ms":600,"p95_ms":600,"max_ms":600}}`
	if string(fb) != wantFB {
		t.Fatalf("first_byte JSON:\n%s\nwant\n%s", fb, wantFB)
	}
	pops, _ := json.Marshal(got.Total.Pops)
	if string(pops) != `{"HKG":1,"LAX":2}` {
		t.Fatalf("pops JSON: %s", pops)
	}
	cache, _ := json.Marshal(got.Total.Cache)
	if string(cache) != `{"HIT":2,"MISS":1}` {
		t.Fatalf("cache JSON: %s", cache)
	}

	// The snapshot's availability carries the same values.
	w = request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	var snap model.Snapshot
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snap) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	var day model.Availability
	for _, cs := range snap.Checks {
		if cs.ID == "home-http" && len(cs.Availability) > 0 {
			day = cs.Availability[0]
		}
	}
	if day.Window != "24h" || day.FirstByte == nil || day.FirstByte.Cached == nil || day.FirstByte.Origin == nil || day.Pops["LAX"] != 2 || day.Cache["HIT"] != 2 {
		t.Fatalf("snapshot availability lost the rollup fields: %+v", day)
	}

	// A reader written before these fields knows only the older shape and
	// must still read the rest: no DisallowUnknownFields anywhere.
	var older struct {
		Total struct {
			Start   time.Time      `json:"start"`
			Seconds int64          `json:"seconds"`
			Samples int64          `json:"samples"`
			Latency *model.Latency `json:"latency"`
		} `json:"total"`
	}
	if e := json.Unmarshal(rollupsBody, &older); e != nil {
		t.Fatal("an older reader cannot read a new rollup:", e)
	}
	if older.Total.Seconds <= 0 || older.Total.Samples != 3 || older.Total.Latency == nil || older.Total.Latency.Samples != 2 {
		t.Fatalf("an older reader lost the fields it knew: %+v", older.Total)
	}
}
