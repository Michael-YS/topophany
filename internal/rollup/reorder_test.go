package rollup

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
	"time"
)

func TestBoundaryEvictionReleasesDiscardedAllocations(t *testing.T) {
	r := New(MaxLimit)
	for seriesID := 0; seriesID < 32; seriesID++ {
		points := make([]open, 4096)
		for i := range points {
			points[i] = open{at: int64(i + 1), stale: 60000}
		}
		s := &series{recent: points}
		// Repeated small removals must also release the invisible prefix.
		for before := int64(2); before <= 4096; before++ {
			r.discardBoundaries(s, before)
			if s.recentCapacity > 2*len(s.recent) {
				t.Fatalf("retained %d boundary slots for %d points", s.recentCapacity, len(s.recent))
			}
		}
		if &s.recent[0] == &points[len(points)-2] || len(s.recent) != 2 {
			t.Fatal("discarded allocation still pinned or predecessor lost")
		}
	}
	bs := make([]bucket, 4096)
	if got := compact(bs, t0.UnixMilli()); cap(got) > 2*len(got) {
		t.Fatal("compacted buckets retain discarded capacity")
	}
}

func TestLateSampleWorkLimitPreservesCountsAcrossRestart(t *testing.T) {
	r := New(MaxLimit)
	n := 10 * maxBoundaryShift
	now := t0.Add(time.Minute)
	for i := 0; i < n; i++ {
		r.Add("c", "o", t0.Add(time.Duration(2*i)*time.Millisecond), "ok", 1, time.Minute, now)
	}
	for restart := 0; restart < 2; restart++ {
		for i := 99; i >= 0; i-- {
			r.Add("c", "o", t0.Add(time.Duration(2*i+1)*time.Millisecond), "fail", 1, time.Minute, now)
		}
		if len(r.series["c"].recent) != n+restart {
			t.Fatal("over-budget samples changed correction boundaries")
		}
		// A nearby late sample still corrects normally after exhaustion.
		at := t0.Add(time.Duration(2*n-3+restart) * time.Millisecond)
		r.Add("c", "o", at, "ok", 1, time.Minute, now)
		got, _ := r.Window("c", t0, now, now)
		if got.Unreconciled != int64(100*(restart+1)) || got.Samples != int64(n+101*(restart+1)) {
			t.Fatal(got)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		r = New(MaxLimit)
		if err := json.Unmarshal(raw, r); err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkLateSampleWorkLimit(b *testing.B) {
	for _, n := range []int{2 * maxBoundaryShift, 100000, 500000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			r := New(MaxLimit)
			s := &series{recent: make([]open, n)}
			for i := range s.recent {
				s.recent[i] = open{at: int64(2*i + 2), stale: 60000}
			}
			s.open = &s.recent[n-1]
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if r.insert(s, open{at: 3, stale: 60000}, 1000000) {
					b.Fatal("over-budget correction accepted")
				}
			}
		})
	}
}

func FuzzLateSampleOrder(f *testing.F) {
	f.Add([]byte{2, 9, 1, 4, 0, 19})
	f.Add([]byte{255, 0, 255, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32 {
			return
		}
		ordered, shuffled := New(MaxLimit), New(MaxLimit)
		now := t0.Add(time.Hour)
		indices := make([]int, len(data))
		add := func(r *Store, i int) {
			r.Add("c", "o", t0.Add(time.Duration(i)*time.Minute), outcomes[int(data[i])%len(outcomes)], int64(data[i]), time.Duration(1+int(data[i])%120)*time.Second, now)
		}
		for i := range data {
			indices[i] = i
			add(ordered, i)
		}
		sort.SliceStable(indices, func(i, j int) bool { return data[indices[i]] < data[indices[j]] })
		for _, i := range indices {
			add(shuffled, i)
		}
		a, _ := ordered.Window("c", t0, now, now)
		b, _ := shuffled.Window("c", t0, now, now)
		if !same(a, b) {
			t.Fatalf("ordered %+v, shuffled %+v", a, b)
		}
	})
}

// The same evidence must give the same time totals whatever order it
// arrives in, including across bucket boundaries and a checkpoint restart.
func TestLateSamplesMatchChronologicalTotalsAcrossRestart(t *testing.T) {
	type sample struct {
		at      time.Time
		outcome string
		stale   time.Duration
	}
	points := make([]sample, 120)
	for i := range points {
		points[i] = sample{t0.Add(time.Duration(i*37) * time.Second), []string{"ok", "fail", "policy", "error", "unknown"}[i%5], time.Duration(10+i%4*30) * time.Second}
	}
	now := points[len(points)-1].at.Add(time.Minute)
	add := func(r *Store, i int) { p := points[i]; r.Add("c", "o", p.at, p.outcome, int64(i), p.stale, now) }
	ordered := New(MaxLimit)
	for i := range points {
		add(ordered, i)
	}
	want, _ := ordered.Window("c", t0, now, now)
	for seed := uint64(0); seed < 20; seed++ {
		r := New(MaxLimit)
		permutation := rand.New(rand.NewPCG(seed, 1)).Perm(len(points))
		for j, i := range permutation {
			add(r, i)
			if j == len(points)/2 {
				r.Settle(now)
				raw, e := json.Marshal(r)
				if e != nil {
					t.Fatal(e)
				}
				r = New(MaxLimit)
				if e = json.Unmarshal(raw, r); e != nil {
					t.Fatal(e)
				}
			}
		}
		got, _ := r.Window("c", t0, now, now)
		if !same(got, want) {
			t.Fatalf("seed %d:\ngot %+v\nwant %+v", seed, got, want)
		}
		if !same(r.Buckets("c", t0, now, FineWidth, now), ordered.Buckets("c", t0, now, FineWidth, now)) {
			t.Fatal("slots differ", seed)
		}
	}
}

func TestLateSamplesCorrectCompactedBuckets(t *testing.T) {
	r := New(MaxLimit)
	r.SetReorderWindow(30 * 24 * time.Hour)
	now := t0.Add(10 * 24 * time.Hour)
	r.Add("c", "o", t0, "ok", 1, 10*time.Minute, now)
	r.Add("c", "o", t0.Add(10*time.Minute), "ok", 1, time.Minute, now)
	r.Settle(now)
	r.Add("c", "o", t0.Add(2*time.Minute), "fail", 1, 3*time.Minute, now)
	got, _ := r.Window("c", t0, t0.Add(time.Hour), now)
	if got.Up != 180 || got.Down != 180 || got.Unmeasured != 3240 || got.Unreconciled != 0 {
		t.Fatal(got)
	}
}

func TestLegacyAndExpiredCorrectionBoundariesAreExplicit(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		r := New(MaxLimit)
		r.SetReorderWindow(time.Minute)
		r.Add("c", "o", t0, "ok", 1, 10*time.Minute, t0)
		r.Add("c", "o", t0.Add(5*time.Minute), "ok", 1, time.Minute, t0.Add(5*time.Minute))
		if legacy {
			raw, _ := json.Marshal(r)
			var d disk
			if e := json.Unmarshal(raw, &d); e != nil {
				t.Fatal(e)
			}
			for i := range d.Series {
				d.Series[i].Recent = nil
				d.Series[i].CorrectionFrom = 0
			}
			raw, _ = json.Marshal(d)
			r = New(MaxLimit)
			if e := json.Unmarshal(raw, r); e != nil {
				t.Fatal(e)
			}
		}
		r.Add("c", "o", t0.Add(2*time.Minute), "fail", 1, time.Minute, t0.Add(6*time.Minute))
		got, _ := r.Window("c", t0, t0.Add(6*time.Minute), t0.Add(6*time.Minute))
		if got.Unreconciled != 1 || got.Samples != 3 {
			t.Fatal(legacy, got)
		}
	}
}

func TestBoundaryBudgetIsReconstructed(t *testing.T) {
	r := New(MinLimit)
	for i := 0; i < 5000; i++ {
		at := t0.Add(time.Duration(i) * time.Second)
		r.Add("c", "o", at, "ok", 1, time.Minute, at)
	}
	raw, e := json.Marshal(r)
	if e != nil || int64(len(raw)) > r.Size() || r.Size() > MinLimit {
		t.Fatal(len(raw), r.Size(), e)
	}
	back := New(MinLimit)
	if e = json.Unmarshal(raw, back); e != nil {
		t.Fatal(e)
	}
	if back.Size() != r.Size() {
		t.Fatal(back.Size(), r.Size())
	}
	now := t0.Add(5000 * time.Second)
	before, _ := r.Window("c", t0, now, now)
	after, _ := back.Window("c", t0, now, now)
	if !same(before, after) {
		t.Fatal(before, after)
	}
}

func TestPartialWindowsAndAmbiguousTimesRemainIncomplete(t *testing.T) {
	r := New(MaxLimit)
	r.Add("c", "o", t0, "ok", 1, 10*time.Minute, t0)
	r.Add("c", "o", t0.Add(5*time.Minute), "ok", 1, time.Minute, t0.Add(5*time.Minute))
	r.Add("c", "o", t0, "fail", 1, time.Minute, t0.Add(5*time.Minute))
	got, _ := r.Window("c", t0.Add(4*time.Minute), t0.Add(5*time.Minute), t0.Add(5*time.Minute))
	if got.Unreconciled != 1 || got.Samples != 0 {
		t.Fatal("partial bucket hid uncertain time", got)
	}
}
