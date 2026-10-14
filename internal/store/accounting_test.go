package store

import (
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

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
