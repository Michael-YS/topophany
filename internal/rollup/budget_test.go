package rollup

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestActiveSeriesMetadataCannotExceedBudget(t *testing.T) {
	r := New(MinLimit)
	r.SetCurrent(func(string) bool { return true })
	for i := 0; i < 1000; i++ {
		r.Add(strings.Repeat("a", 64)+fmt.Sprintf("/check-%04d", i), strings.Repeat("o", 96), t0, "ok", 1, time.Minute, t0)
	}
	check := func(r *Store) {
		t.Helper()
		raw, e := json.Marshal(r)
		if e != nil || int64(len(raw)) > r.Size() || r.Size() > r.limit {
			t.Fatalf("encoded=%d accounted=%d limit=%d err=%v", len(raw), r.Size(), r.limit, e)
		}
	}
	check(r)
	r.Settle(t0.Add(time.Hour))
	check(r)
	raw, _ := json.Marshal(r)
	back := New(MinLimit / 2)
	if e := json.Unmarshal(raw, back); e != nil {
		t.Fatal(e)
	}
	back.Settle(t0.Add(time.Hour))
	check(back)
}

func TestMetadataEvictionPreservesObserverShares(t *testing.T) {
	r := New(MinLimit)
	r.SetCurrent(func(string) bool { return true })
	r.Add("quiet", "quiet", t0, "ok", 1, time.Minute, t0)
	for i := 0; i < 1000; i++ {
		r.Add(fmt.Sprintf("busy-%04d", i), "busy", t0, "ok", 1, time.Minute, t0)
	}
	if r.Since("quiet").IsZero() || r.Size() > r.limit {
		t.Fatal("busy observer evicted quiet metadata", r.Size())
	}
}
