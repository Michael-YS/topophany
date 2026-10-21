package store

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

type accounting struct {
	receipts      int
	stateBytes    int64
	rowBytes      int64
	scopeBytes    map[string]int64
	scopeReceipts map[string]int
}

func (s *Store) accounting() accounting {
	return accounting{len(s.seen), s.stateBytes, s.rowBytes, maps.Clone(s.scopeBytes), maps.Clone(s.scopeReceipts)}
}

// checkAccounting holds what the store keeps up to date as it goes to what
// it would derive afresh, and finds no receipt left that should have gone.
func checkAccounting(t *testing.T, s *Store) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	refs := map[string]int{}
	rows := map[string]model.Observation{}
	for _, v := range s.rows {
		refs[v.ID]++
		rows[v.ID] = v
	}
	for _, tail := range s.tails {
		for _, v := range tail {
			refs[v.ID]++
			rows[v.ID] = v
		}
	}
	if !maps.Equal(refs, s.rowRefs) || len(rows) != len(s.rowSizes) || len(rows) != len(s.rowHashes) || len(rows) != len(s.rowObservers) {
		t.Fatal("incremental indexes differ from retained rows")
	}
	for id, v := range rows {
		raw, err := json.Marshal(v)
		if err != nil || s.rowSizes[id] != int64(len(raw))+1 || s.rowHashes[id] != observationHash(v) || s.rowObservers[id] != v.Observer {
			t.Fatal("retained row accounting mismatch", id, err)
		}
	}
	kept := s.accounting()
	expiry := s.options.Now().Add(-s.options.Retention - receiptGrace)
	for id, r := range s.seen {
		if r.At.Before(expiry) {
			t.Fatal("an expired receipt was kept", id)
		}
	}
	s.recount()
	s.prune()
	derived := s.accounting()
	if kept.receipts != derived.receipts || kept.stateBytes != derived.stateBytes || kept.rowBytes != derived.rowBytes || !maps.Equal(kept.scopeBytes, derived.scopeBytes) || !maps.Equal(kept.scopeReceipts, derived.scopeReceipts) {
		t.Fatalf("accounting kept %+v, derived %+v", kept, derived)
	}
}

// Receipts expire and rows go without the store visiting every receipt it
// holds, and what it counts stays what it holds: through concurrent
// ingestion from two observers with viewers reading, receipts expiring,
// history at capacity, a reload and a restart.
func TestAccountingKeptAsItGoesIsWhatIsHeld(t *testing.T) {
	c := testkit.Config()
	dir := t.TempDir()
	var mu sync.Mutex
	clock := time.Now().UTC().Add(-48 * time.Hour)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	o := Options{Observers: []string{"home", "cloud"}, MaxHistory: 300, Retention: time.Hour, Now: now}
	s := openTest(t, dir, o)
	var seq [2]uint64
	put := func(t *testing.T, observer int, checks []string) {
		for i := 0; i < 20; i++ {
			at := now()
			vs := []model.Observation{}
			for _, id := range checks {
				seq[observer]++
				vs = append(vs, testkit.Observation(c, id, seq[observer], at, "ok"))
			}
			if e := s.Put(batch(c, vs...)); e != nil {
				t.Error(e)
				return
			}
		}
	}
	for round := 0; round < 12; round++ {
		var wg sync.WaitGroup
		wg.Go(func() { put(t, 0, []string{"home-http", "home-ssh", "primary-http"}) })
		wg.Go(func() { put(t, 1, []string{"cloud-http"}) })
		wg.Go(func() {
			for i := 0; i < 20; i++ {
				s.StateView()
			}
		})
		wg.Wait()
		checkAccounting(t, s)
		mu.Lock()
		clock = clock.Add(25 * time.Minute)
		mu.Unlock()
		if round == 6 {
			s.Close()
			s = openTest(t, dir, o)
			checkAccounting(t, s)
		}
	}
	if len(s.seen) == int(seq[0]+seq[1]) {
		t.Fatal("no receipt expired")
	}
	if len(s.rows) > o.MaxHistory {
		t.Fatal("history beyond capacity", len(s.rows))
	}
}

// A store says how much of each observer's share it holds and, at the last
// hour's rate, where its receipts settle and when they fill the share.
func TestUsageSaysWhereReceiptsSettle(t *testing.T) {
	c := testkit.Config()
	clock := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Observers: []string{"home", "cloud"}, MaxReceipts: 2000, Retention: time.Hour, Now: func() time.Time { return clock }})
	put := func(id string, n int) {
		for i := 0; i < n; i += 100 {
			vs := []model.Observation{}
			for j := i; j < min(i+100, n); j++ {
				v := testkit.Observation(c, id, uint64(j+1), clock, "ok")
				vs = append(vs, v)
			}
			if e := s.Put(batch(c, vs...)); e != nil {
				t.Fatal(e)
			}
		}
	}
	put("home-http", 600)
	put("cloud-http", 10)
	u := s.Usage()
	if u.Receipts != 610 || u.Hours != 2 || len(u.Observers) != 2 {
		t.Fatalf("%+v", u)
	}
	cloud, home := u.Observers[0], u.Observers[1]
	if home.Observer != "home" || home.Receipts != 600 || home.ReceiptLimit != 1000 || home.LastHour != 600 || home.Settles != 1200 || !home.Fills || home.FullIn != 40*time.Minute {
		t.Fatalf("home %+v", home)
	}
	if cloud.Observer != "cloud" || cloud.Receipts != 10 || cloud.Settles != 20 || cloud.Fills || cloud.Bytes <= 0 {
		t.Fatalf("cloud %+v", cloud)
	}
	if line := u.String(); !strings.Contains(line, "home 600 of 1000 receipts") || !strings.Contains(line, "full in 40m") {
		t.Fatal(line)
	}
	// An hour on, the rate is what came in since: none.
	clock = clock.Add(61 * time.Minute)
	if u := s.Usage(); u.Observers[1].LastHour != 0 || u.Observers[1].Fills {
		t.Fatalf("%+v", u.Observers[1])
	}
}

func TestRowOwnershipThroughAllocationAndContractChanges(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	options := Options{MaxHistory: 12, Observers: []string{"home", "cloud"}, Contracts: config.Contracts(c), Revision: config.Revision(c), Stale: config.Stale(c), Now: func() time.Time { return now }}
	s := openTest(t, dir, options)
	for i := 1; i <= 90; i++ {
		// Mix late samples with newer ones; pruning must preserve the newest per owner.
		at := now.Add(-time.Duration((i*17)%90) * time.Second)
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", uint64(i), at, "ok"), testkit.Observation(c, "cloud-http", uint64(i), at, "ok"))); err != nil {
			t.Fatal(err)
		}
	}
	checkAccounting(t, s)
	s.SetAllocation([]string{"home", "cloud"}, map[string]int{"home": 3, "cloud": 1})
	s.SetCurrent(config.Revision(c), config.Contracts(c), config.Stale(c))
	checkAccounting(t, s)
	counts := map[string]int{}
	for _, v := range s.rows {
		counts[v.Observer]++
	}
	// A larger reservation cannot recreate raw rows already evicted.
	if counts["home"] != 6 || counts["cloud"] != 3 {
		t.Fatal(counts)
	}
	next := config.Contracts(c)
	next["home-http"] = "changed"
	s.SetCurrent("changed", next, config.Stale(c))
	checkAccounting(t, s)
	s.SetAllocation([]string{"cloud"}, nil)
	s.prune()
	checkAccounting(t, s)
	for _, v := range s.rows {
		if v.Observer != "cloud" {
			t.Fatal("removed observer history retained")
		}
	}
	now = now.Add(48 * time.Hour)
	checkAccounting(t, s)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	options = s.options
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, options)
	checkAccounting(t, s)
}

func TestRecentRateAcrossExpiryRollbackAndRestart(t *testing.T) {
	c := testkit.Config()
	clock := time.Now().UTC()
	start := clock
	dir := t.TempDir()
	o := Options{Now: func() time.Time { return clock }, Retention: time.Hour}
	s := openTest(t, dir, o)
	put := func(seq uint64) {
		t.Helper()
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", seq, clock, "ok"))); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want int) {
		t.Helper()
		for range 3 {
			if got := s.Usage().Observers[0].LastHour; got != want {
				t.Fatalf("at %s: recent=%d want %d", clock.Sub(start), got, want)
			}
		}
		checkAccounting(t, s)
	}
	put(1)
	clock = start.Add(time.Hour)
	check(1) // The inclusive hour boundary is preserved exactly.
	clock = clock.Add(time.Nanosecond)
	check(0)
	clock = start.Add(30 * time.Minute)
	check(1)
	put(2)
	clock = start.Add(20 * time.Minute)
	put(3) // New receipts after a rollback remain ordered for both cursors.
	check(3)
	clock = start.Add(85 * time.Minute)
	check(1)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, o)
	check(1)
	clock = start.Add(4 * time.Hour)
	check(0)
}

func TestHeartbeatByteAccountingTracksReplacements(t *testing.T) {
	c := testkit.Config()
	clock := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Now: func() time.Time { return clock }})
	for i := range 8 {
		h := model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: clock}
		if i%2 == 0 {
			h.Delivery = []model.DeliveryStatus{{Collector: "0123456789abcdef", ReportedAt: clock}}
		}
		b := batch(c)
		b.Heartbeat = &h
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
		checkAccounting(t, s)
		clock = clock.Add(2 * time.Second)
	}
}

func BenchmarkUsageRetainedReceipts(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			now := time.Now().UTC()
			s := &Store{options: Options{Now: func() time.Time { return now }}, seen: map[string]receipt{}}
			for i := range n {
				id := fmt.Sprint(i)
				s.seen[id] = receipt{At: now, Observer: "home"}
				s.receiptQueue = append(s.receiptQueue, id)
			}
			s.recountRecent(now)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s.Usage()
			}
		})
	}
}
