package rollup

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// feed adds a sample every interval over [from, to), each saying what
// outcome says at its moment and taking 100 ms, as read at now or, when
// now is zero, as each arrives.
func feed(r *Store, key string, from, to time.Time, every, stale time.Duration, outcome func(time.Time) string, now time.Time) {
	for at := from; at.Before(to); at = at.Add(every) {
		if o := outcome(at); o != "" {
			arrived := now
			if arrived.IsZero() {
				arrived = at
			}
			r.Add(key, "", at, o, 100, stale, arrived)
		}
	}
}

func always(o string) func(time.Time) string { return func(time.Time) string { return o } }

// Each sample's outcome stands until the next sample or until it goes
// stale. Time with no current sample is unmeasured, failures are down,
// policy violations degraded, monitor-side errors unknown, and none of
// those is counted as up.
func TestTimeStandsForTheNewestCurrentSample(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(4 * time.Hour)
	// 0:00–1:00 passing, 1:00–1:20 failing, 1:20–1:30 a policy violation,
	// 1:30–1:40 monitor errors, then silence until 2:00, passing after.
	feed(r, "c", t0, t0.Add(4*time.Hour), 30*time.Second, 90*time.Second, func(at time.Time) string {
		switch d := at.Sub(t0); {
		case d < time.Hour:
			return "ok"
		case d < 80*time.Minute:
			return "fail"
		case d < 90*time.Minute:
			return "policy"
		case d < 100*time.Minute:
			return "error"
		case d < 2*time.Hour:
			return ""
		}
		return "ok"
	}, now)
	got, since := r.Window("c", t0, now, now)
	if !since.IsZero() {
		t.Fatal("a series held from the window's start has no since", since)
	}
	// The last error stands for 90 seconds into the silence, and the
	// newest sample until now.
	want := [5]int64{3 * 3600, 20 * 60, 10 * 60, 11 * 60, 19 * 60}
	if g := [5]int64{got.Up, got.Down, got.Degraded, got.Unknown, got.Unmeasured}; g != want {
		t.Fatalf("time by class %v, want %v", g, want)
	}
	if got.Seconds != 4*3600 || got.Measured() != got.Up+got.Down+got.Degraded {
		t.Fatal("window length or measured time wrong", got.Seconds, got.Measured())
	}
	if got.Samples != 440 || got.Outcomes["ok"] != 360 || got.Outcomes["fail"] != 40 || got.Outcomes["policy"] != 20 || got.Outcomes["error"] != 20 {
		t.Fatalf("samples counted wrongly: %d %v", got.Samples, got.Outcomes)
	}
	if got.Latency == nil || got.Latency.Samples != 360 || got.Latency.Max != 100 || got.Latency.P50 < 90 || got.Latency.P50 > 100 {
		t.Fatalf("latency of passing samples %+v", got.Latency)
	}
}

// A sample no newer than the newest is counted, but the time it would
// stand for is already the newest's and the ones before it.
func TestLateSamplesCountWithoutTime(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(10 * time.Minute)
	r.Add("c", "", t0, "ok", 10, time.Minute, now)
	r.Add("c", "", t0.Add(5*time.Minute), "ok", 10, time.Minute, now)
	r.Add("c", "", t0.Add(2*time.Minute), "fail", 10, time.Minute, now)
	got, _ := r.Window("c", t0, now, now)
	if got.Up != 120 || got.Down != 0 || got.Samples != 3 || got.Outcomes["fail"] != 1 {
		t.Fatalf("late sample changed the time: %+v", got)
	}
}

// Buckets are five minutes wide for two days, then an hour, then a day,
// and merging them changes no total.
func TestBucketsWidenWithAgeWithoutChangingTotals(t *testing.T) {
	r := New(MaxLimit)
	end := t0.Add(3 * 24 * time.Hour)
	feed(r, "c", t0, end, time.Minute, 3*time.Minute, func(at time.Time) string {
		if at.Minute() < 6 {
			return "fail"
		}
		return "ok"
	}, time.Time{})
	widths := func() map[int64]int {
		m := map[int64]int{}
		for _, b := range r.series["c"].buckets {
			m[b.width/1000]++
		}
		return m
	}
	before, _ := r.Window("c", t0, end, end)
	r.Settle(end)
	if w := widths(); w[300] != 576 || w[3600] != 24 || w[86400] != 0 {
		t.Fatalf("widths after three days %v", w)
	}
	after, _ := r.Window("c", t0, end, end)
	if !same(before, after) {
		t.Fatalf("settling changed the totals:\n%+v\n%+v", before, after)
	}
	later := t0.Add(40 * 24 * time.Hour)
	r.Settle(later)
	if w := widths(); w[86400] != 3 || w[3600]+w[300] != 0 {
		t.Fatalf("widths after forty days %v", w)
	}
	again, _ := r.Window("c", t0, end, later)
	if !same(before, again) {
		t.Fatalf("days hold what hours held:\n%+v\n%+v", before, again)
	}
	// A late sample lands in whatever bucket now holds its moment.
	r.Add("c", "", t0.Add(time.Hour+time.Second), "ok", 5, 3*time.Minute, later)
	if w := widths(); w[86400] != 3 || len(r.series["c"].buckets) != 3 {
		t.Fatal("late sample made a bucket", w)
	}
}

func same(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// A window that begins inside a bucket takes the share of it that it
// covers, and one that ends inside the newest bucket takes it whole.
func TestWindowEdges(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(10*24*time.Hour + 30*time.Second)
	feed(r, "c", t0, now, time.Minute, 3*time.Minute, always("ok"), now)
	r.Settle(now)
	day, _ := r.Window("c", now.Add(-24*time.Hour), now, now)
	if day.Seconds != 86400 || day.Up != 86400 || day.Unmeasured != 0 {
		t.Fatalf("a day measured throughout %+v", day)
	}
	// The week begins inside an hourly bucket, 30 seconds into it.
	week, _ := r.Window("c", now.Add(-7*24*time.Hour), now, now)
	if week.Up != 7*86400 || week.Samples != 7*1440+1 {
		t.Fatalf("a week measured throughout %+v", week)
	}
	month, since := r.Window("c", now.Add(-30*24*time.Hour), now, now)
	if !since.Equal(t0) || month.Up != int64(now.Sub(t0)/time.Second) || month.Unmeasured != month.Seconds-month.Up {
		t.Fatalf("a month of which ten days are held %+v since %v", month, since)
	}
}

// Slots are as wide as the step, except where the store keeps wider
// buckets; the open sample's time counts in the slots it stands in.
func TestBucketsLayOutSlots(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(40*24*time.Hour + 90*time.Minute)
	feed(r, "c", now.Add(-35*24*time.Hour), now, 10*time.Minute, 15*time.Minute, always("ok"), now)
	r.Settle(now)
	slots := r.Buckets("c", now.Add(-34*24*time.Hour), now, time.Hour, now)
	if slots[0].Seconds != 86400 || !slots[0].Start.Equal(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("the oldest slot is the day kept whole: %+v", slots[0])
	}
	last := slots[len(slots)-1]
	if last.Seconds != 30*60 || last.Up != 30*60 || last.Samples != 3 {
		t.Fatalf("the newest slot runs until now: %+v", last)
	}
	for i := 1; i < len(slots); i++ {
		if !slots[i].Start.Equal(slots[i-1].Start.Add(time.Duration(slots[i-1].Seconds) * time.Second)) {
			t.Fatal("slots leave a gap or overlap at", i)
		}
	}
	if fine := r.Buckets("c", now.Add(-time.Hour), now, FineWidth, now); len(fine) != 12 || fine[0].Seconds != 300 {
		t.Fatalf("five-minute slots for the last hour: %d", len(fine))
	}
	if none := r.Buckets("missing", now.Add(-2*time.Hour), now, time.Hour, now); len(none) != 3 || none[0].Unmeasured != 3600 {
		t.Fatalf("a series not held lays out unmeasured slots: %+v", none)
	}
}

// Under its cap, a store sheds series no longer counted first, the least
// recently added to first, and then the oldest buckets of any series.
func TestStoreShedsUnderItsCap(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(5 * time.Hour)
	for _, key := range []string{"old/a", "cur/b", "cur/c"} {
		feed(r, key, t0, now, time.Minute, 3*time.Minute, always("ok"), now)
	}
	r.Add("old/a", "", now, "ok", 1, time.Minute, now.Add(-time.Hour))
	r.Settle(now)
	full := r.Size()
	r.SetCurrent(func(key string) bool { return strings.HasPrefix(key, "cur/") })
	r.limit = full - 1
	r.Settle(now)
	if keys := r.Keys(); !slices.Equal(keys, []string{"cur/b", "cur/c"}) {
		t.Fatal("kept", keys)
	}
	r.limit = r.Size() - 2*160
	r.Settle(now)
	if !r.Since("cur/b").Equal(t0.Add(5*time.Minute)) || !r.Since("cur/c").Equal(t0.Add(5*time.Minute)) {
		t.Fatal("did not shed the oldest bucket of each", r.Since("cur/b"), r.Since("cur/c"))
	}
	if r.Size() > r.limit {
		t.Fatal("over its cap", r.Size(), r.limit)
	}
	got, since := r.Window("cur/b", t0, now, now)
	if !since.Equal(t0.Add(5*time.Minute)) || got.Unmeasured != 300 {
		t.Fatalf("shed time reads as unmeasured: %+v", got)
	}
}

// An observer whose samples fill the cap sheds its own oldest rollups,
// not the older ones of another observer that holds less, and so it is
// after the store is written and read back.
func TestOneObserverCannotShedAnother(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(5 * 24 * time.Hour)
	add := func(key, owner string, from time.Time) {
		for at := from; at.Before(now); at = at.Add(time.Minute) {
			r.Add(key, owner, at, "ok", 100, 3*time.Minute, now)
		}
	}
	add("c/quiet", "quiet", t0)
	for i := range 4 {
		add(fmt.Sprintf("c/busy-%d", i), "busy", t0.Add(time.Hour))
	}
	r.Settle(now)
	r.limit = r.Size() - 40*160
	r.Settle(now)
	if !r.Since("c/quiet").Equal(t0) {
		t.Fatal("the quiet observer's rollups were shed", r.Since("c/quiet"))
	}
	if !r.Since("c/busy-0").After(t0.Add(time.Hour)) || r.Size() > r.limit {
		t.Fatal("the busy observer's rollups were not shed", r.Since("c/busy-0"), r.Size(), r.limit)
	}
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	back := New(r.limit - 40*160)
	if e = json.Unmarshal(raw, back); e != nil {
		t.Fatal(e)
	}
	back.Settle(now)
	if !back.Since("c/quiet").Equal(t0) || !back.Since("c/busy-0").After(r.Since("c/busy-0")) {
		t.Fatal("owners not kept through a write", back.Since("c/quiet"), back.Since("c/busy-0"))
	}
	// Once it holds more than the rest, the quiet observer sheds too.
	back.limit = 4 * 160 * 30
	back.Settle(now)
	if back.Since("c/quiet").Equal(t0) || back.Size() > back.limit {
		t.Fatal("an observer holding the most was not shed", back.Since("c/quiet"), back.Size())
	}
}

// What a store writes it reads back, and it refuses what it could not have
// written.
func TestStoreRoundTripsAndRefusesCorruption(t *testing.T) {
	r := New(MaxLimit)
	now := t0.Add(50 * 24 * time.Hour)
	feed(r, "k/c", t0, now, 7*time.Minute, 10*time.Minute, func(at time.Time) string {
		return []string{"ok", "ok", "ok", "fail", "policy", "error"}[at.Unix()/60%6]
	}, now)
	r.Settle(now)
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	back := New(MaxLimit)
	if e = json.Unmarshal(raw, back); e != nil {
		t.Fatal(e)
	}
	for _, span := range []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 45 * 24 * time.Hour} {
		a, _ := r.Window("k/c", now.Add(-span), now, now)
		b, _ := back.Window("k/c", now.Add(-span), now, now)
		if !same(a, b) {
			t.Fatalf("%v differs after reading back:\n%+v\n%+v", span, a, b)
		}
	}
	if again, _ := json.Marshal(back); string(again) != string(raw) {
		t.Fatal("rewriting changed it")
	}
	if back.Size() != r.Size() {
		t.Fatal("size not restored", back.Size(), r.Size())
	}
	if int64(len(raw)) > r.Size() {
		t.Fatal("encoded size exceeds its bound", len(raw), r.Size())
	}
	for _, bad := range []string{
		`{"schema":2,"series":[]}`,
		`{"schema":1,"series":[{"k":"","f":0,"u":0,"b":[]}]}`,
		`{"schema":1,"series":[{"k":"a","f":0,"u":0,"b":[{"s":1,"w":300,"t":[0,0,0,0],"n":[0,0,0,0,0]}]}]}`,
		`{"schema":1,"series":[{"k":"a","f":0,"u":0,"b":[{"s":600,"w":300,"t":[0,0,0,0],"n":[0,0,0,0,0]},{"s":300,"w":300,"t":[0,0,0,0],"n":[0,0,0,0,0]}]}]}`,
		`{"schema":1,"series":[{"k":"a","f":0,"u":0,"b":[{"s":300,"w":600,"t":[0,0,0,0],"n":[0,0,0,0,0]}]}]}`,
		`{"schema":1,"series":[{"k":"a","f":0,"u":0,"b":[{"s":300,"w":300,"t":[-1,0,0,0],"n":[0,0,0,0,0]}]}]}`,
		`{"schema":1,"series":[{"k":"a","f":0,"u":0,"b":[{"s":300,"w":300,"t":[0,0,0,0],"n":[1,0,0,0,0],"l":[2,10,5,20,1]}]}]}`,
		`{"schema":1,"series":[{"k":"a","f":0,"u":0,"o":[1,0,0],"b":[]}]}`,
		`{"schema":1,"series":[{"k":"a","f":0,"u":0,"o":[1,1,4],"b":[]}]}`,
	} {
		if e := json.Unmarshal([]byte(bad), New(MaxLimit)); e == nil {
			t.Fatal("accepted", bad)
		}
	}
}

// Percentiles from merged bins fall within a bin's width of the exact
// nearest-rank percentile, and never past the slowest sample.
func TestLatencyPercentilesFromBins(t *testing.T) {
	var h histogram
	ms := []int64{}
	for i := range 1000 {
		v := int64(20 + (i*7919)%480)
		if i%50 == 0 {
			v = 3000
		}
		ms = append(ms, v)
		h.add(v)
	}
	slices.Sort(ms)
	for _, q := range []float64{.5, .95} {
		exact := ms[int(math.Ceil(q*float64(len(ms))))-1]
		got := h.quantile(q)
		if math.Abs(float64(got-exact)) > float64(exact)*0.2 {
			t.Fatalf("p%v %d, exact %d", q*100, got, exact)
		}
	}
	var halves [2]histogram
	for i, v := range ms {
		halves[i%2].add(v)
	}
	halves[0].merge(halves[1])
	if halves[0].quantile(.95) != h.quantile(.95) || halves[0].max != 3000 || halves[0].n != 1000 {
		t.Fatal("merged bins differ from one histogram")
	}
	var one histogram
	one.add(250)
	if one.quantile(.5) > 250 {
		t.Fatal("past the slowest sample", one.quantile(.5))
	}
}
