package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A receipt goes once no sample it could match can be accepted, an hour
// after the ingestion window, and releases its capacity; its ID stays
// bound by its boot's floor, across restarts with or without a checkpoint.
func TestReceiptsExpireButIDsStayBound(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		c := testkit.Config()
		start := time.Now().UTC()
		now := start
		dir := t.TempDir()
		opts := Options{Observers: []string{"home", "cloud"}, Retention: time.Minute, Now: func() time.Time { return now }}
		s := openTest(t, dir, opts)
		for i := uint64(1); i <= 5; i++ {
			if e := s.Put(batch(c, testkit.Observation(c, "home-http", i, now, "ok"))); e != nil {
				t.Fatal(e)
			}
		}
		now = now.Add(time.Hour)
		s.prune()
		if len(s.seen) != 5 {
			t.Fatal("a receipt expired inside the ingestion window and an hour", len(s.seen))
		}
		now = now.Add(2 * time.Minute)
		if e := s.Put(batch(c, testkit.Observation(c, "home-http", 6, now, "ok"))); e != nil {
			t.Fatal(e)
		}
		if len(s.seen) != 1 || s.floors["home.boot"] != 5 || s.scopeReceipts["home"] != 1 {
			t.Fatal("expired receipts kept or capacity not released", len(s.seen), s.floors, s.scopeReceipts)
		}
		if checkpoint {
			if e := s.Checkpoint(); e != nil {
				t.Fatal(e)
			}
		}
		s.Close()
		s = openTest(t, dir, opts)
		reused := testkit.Observation(c, "home-http", 3, now, "fail")
		if e := s.Put(batch(c, reused)); !errors.Is(e, ErrConflict) {
			t.Fatal("an expired ID was accepted again", checkpoint, e)
		}
		if e := s.Put(batch(c, testkit.Observation(c, "home-http", 7, now, "ok"))); e != nil {
			t.Fatal("the next sample of the boot refused", e)
		}
		other := testkit.Observation(c, "home-http", 1, now, "ok")
		other.Boot, other.ID = "later", "home.later.1"
		if e := s.Put(batch(c, other)); e != nil {
			t.Fatal("another boot's first sample refused", e)
		}
	}
}

// An observer delivering samples without end is never refused for its
// quota: receipts older than the ingestion window and an hour go, so what
// it holds stays the window's worth.
func TestCollectionNeverPausesAtTheReceiptQuota(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Observers: []string{"home", "cloud"}, MaxStateBytes: 4 << 20, MaxHistory: 1000, Retention: time.Minute, Now: func() time.Time { return now }})
	seq := uint64(0)
	for range 12 * 60 * 60 / 200 {
		b := batch(c)
		for range 100 {
			seq++
			now = now.Add(2 * time.Second)
			b.Observations = append(b.Observations, testkit.Observation(c, "home-http", seq, now, "ok"))
		}
		if e := s.Put(b); e != nil {
			t.Fatalf("refused after %d samples: %v", seq-100, e)
		}
	}
	if len(s.seen) > 61*30+100 {
		t.Fatal("receipts outlived the window", len(s.seen))
	}
}

// Rollups count each accepted sample once: replaying the journal after a
// crash, or after a checkpoint the journal outlived, counts nothing twice,
// and neither does a sample whose receipt expired in between.
func TestRollupsCountEachSampleOnce(t *testing.T) {
	c := testkit.Config()
	x, _ := config.Check(c, "home-http")
	contract := config.Contract(c, x)
	start := time.Now().UTC().Truncate(time.Hour)
	now := start
	dir := t.TempDir()
	opts := Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Retention: time.Minute, Now: func() time.Time { return now }}
	s := openTest(t, dir, opts)
	put := func(from, to uint64, outcome string) {
		t.Helper()
		for i := from; i <= to; i++ {
			now = start.Add(time.Duration(i) * time.Minute)
			if e := s.Put(batch(c, testkit.Observation(c, "home-http", i, now, outcome))); e != nil {
				t.Fatal(e)
			}
		}
	}
	read := func() model.Availability {
		t.Helper()
		got := s.Availability("home-http", contract, now.Add(30*time.Second))
		if len(got) != 3 || got[0].Window != "24h" {
			t.Fatal("availability windows", got)
		}
		return got[0]
	}
	put(1, 60, "ok")
	put(61, 70, "fail")
	want := read()
	if want.Samples != 70 || want.Up != 60*60 || want.Down != 9*60+30 {
		t.Fatalf("availability of an hour up and ten minutes failing: %+v", want.Rollup)
	}
	journal := filepath.Join(dir, "journal.jsonl")
	s.Close()
	s = openTest(t, dir, opts)
	if got := read(); !sameJSON(got, want) {
		t.Fatalf("replay changed the rollups:\n%+v\n%+v", got, want)
	}
	wal, _ := os.ReadFile(journal)
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if e := os.WriteFile(journal, wal, 0600); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, dir, opts)
	if got := read(); !sameJSON(got, want) {
		t.Fatalf("a checkpoint and the journal it outlived counted twice:\n%+v\n%+v", got, want)
	}
	// Two hours on, every receipt so far has expired. A checkpoint the
	// journal outlives, and a replay of it, still count nothing twice.
	put(190, 190, "ok")
	want = read()
	wal, _ = os.ReadFile(journal)
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	if len(s.seen) != 1 {
		t.Fatal("receipts did not expire", len(s.seen))
	}
	s.Close()
	if e := os.WriteFile(journal, wal, 0600); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, dir, opts)
	if got := read(); !sameJSON(got, want) || got.Samples != 71 {
		t.Fatalf("an expired sample replayed into the rollups:\n%+v\n%+v", got, want)
	}
}

// Rollups follow the contract, as history does: a check whose contract
// changes starts a new series, and the old one is shed first.
func TestRollupsFollowTheContract(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Now: func() time.Time { return now }})
	if e := s.Put(batch(c, testkit.Observation(c, "home-http", 1, now, "ok"))); e != nil {
		t.Fatal(e)
	}
	edited := testkit.Config()
	edited.Checks[0].HTTP.Status = []int{204}
	s.SetCurrent(config.Revision(edited), config.Contracts(edited), config.Stale(edited))
	if got := s.Availability("home-http", config.Contracts(edited)["home-http"], now); got != nil {
		t.Fatal("a new contract inherited the old one's rollups", got)
	}
	if got := s.Availability("home-http", config.Contracts(c)["home-http"], now); len(got) != 3 {
		t.Fatal("the old contract's rollups went at once", got)
	}
	if !s.rollups.Since(config.Contracts(c)["home-http"]+"/home-http").Equal(now.Truncate(time.Millisecond)) || s.currentKey(config.Contracts(c)["home-http"]+"/home-http") {
		t.Fatal("the old series reads as current")
	}
	// A schema 1 sample counted under the topology joins its contract.
	legacy := testkit.Observation(edited, "home-http", 2, now, "ok")
	legacy.Contract, legacy.Revision = "", config.Revision(edited)
	if key := s.rollKey(legacy); key != config.Contracts(edited)["home-http"]+"/home-http" {
		t.Fatal("legacy sample keyed", key)
	}
}

// A checkpoint holds the rollups within their cap.
func TestCheckpointedRollupsStayWithinTheirCap(t *testing.T) {
	c := testkit.Config()
	start := time.Now().UTC()
	now := start
	dir := t.TempDir()
	opts := Options{RollupBytes: 64 << 10, Retention: time.Hour, Now: func() time.Time { return now }}
	s := openTest(t, dir, opts)
	for i := uint64(1); i <= 300; i++ {
		now = start.Add(time.Duration(i) * 5 * time.Minute)
		if e := s.Put(batch(c, testkit.Observation(c, "home-http", i, now, "ok"), testkit.Observation(c, "cloud-http", i, now, "ok"))); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(s.rollups)
	if len(raw) > 64<<10 || s.rollups.Size() > 64<<10 {
		t.Fatal("rollups over their cap", len(raw), s.rollups.Size())
	}
	since := s.rollups.Since(config.Contract(c, c.Checks[0]) + "/home-http")
	if !since.After(start.Add(time.Hour)) {
		t.Fatal("nothing shed", since)
	}
}

func sameJSON(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// A worker picks its boot IDs, and each boot whose receipts expire leaves
// a floor. One that changes its boot without end cannot fill its
// observer's share with floors for good: past an eighth of the share, the
// floors raised longest ago go, while a boot still delivering keeps its
// floor and its IDs stay bound, across a restart.
func TestBootRotationCannotFillAnObserversShare(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	opts := Options{Observers: []string{"home", "cloud"}, MaxStateBytes: 1 << 20, Retention: time.Minute, Now: func() time.Time { return now }}
	s := openTest(t, dir, opts)
	limit := s.floorLimit()
	if limit != (1<<20)/2/8/floorBytes {
		t.Fatal(limit)
	}
	held := func(s *Store) int {
		n := 0
		for boot := range s.floors {
			if receiptObserver(boot+".0") == "home" {
				n++
			}
		}
		return n
	}
	steady := uint64(0)
	sample := func(boot string, seq uint64) model.Observation {
		v := testkit.Observation(c, "home-http", seq, now, "ok")
		v.Boot, v.ID = boot, fmt.Sprintf("home.%s.%d", boot, seq)
		return v
	}
	// Enough boots for their floors to fill the share, were they all kept.
	for round := range 50 {
		b := batch(c)
		for i := range 99 {
			b.Observations = append(b.Observations, sample(fmt.Sprintf("r%dx%d", round, i), 1))
		}
		steady++
		b.Observations = append(b.Observations, sample("steady", steady))
		if e := s.Put(b); e != nil {
			t.Fatalf("round %d refused with %d floors held: %v", round, held(s), e)
		}
		now = now.Add(time.Minute + receiptGrace + time.Second)
	}
	s.prune()
	if n := held(s); n > limit || n < limit/2 {
		t.Fatal("floors held", n, "of", limit)
	}
	if s.floors["home.steady"] != steady {
		t.Fatal("the boot still delivering lost its floor", s.floors["home.steady"], steady)
	}
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s = openTest(t, dir, opts)
	if n := held(s); n > limit {
		t.Fatal("floors held after a restart", n)
	}
	if e := s.Put(batch(c, sample("steady", 1))); !errors.Is(e, ErrConflict) {
		t.Fatal("a delivering boot's expired ID was accepted again", e)
	}
	if e := s.Put(batch(c, sample("steady", steady+1), sample("fresh", 1))); e != nil {
		t.Fatal("ordinary samples refused", e)
	}
}

// A worker picks its boot IDs, so it can have the store hold as many
// floors as its observer's share allows, and every write prunes them
// under the lock all observers' ingestion waits on. Counting them, and
// shedding the oldest as another boot's receipt expires, costs about the
// same with a full share of floors as with a few: no write visits each
// floor or sorts them all.
func TestFloorsAreKeptWithoutVisitingThemAll(t *testing.T) {
	now := time.Now().UTC()
	cost := func(n int) time.Duration {
		s := openTest(t, t.TempDir(), Options{Observers: []string{"home"}, MaxStateBytes: int64(n) * floorShare * floorBytes, Now: func() time.Time { return now }})
		if s.floorLimit() != n {
			t.Fatal(s.floorLimit(), n)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for i := range n {
			s.raiseFloor(fmt.Sprintf("home.old%d", i), 1, now.Add(time.Duration(i-n)*time.Second))
		}
		s.prune()
		best := time.Duration(1 << 62)
		for i := range 64 {
			start := time.Now()
			s.raiseFloor(fmt.Sprintf("home.new%d", i), 1, now)
			s.prune()
			best = min(best, time.Since(start))
		}
		if len(s.floors) != n || s.scopeBytes["home"] != int64(n)*floorBytes {
			t.Fatal("floors held", len(s.floors), s.scopeBytes)
		}
		if _, ok := s.floors["home.old63"]; ok {
			t.Fatal("an old floor outlived the ones shed for new boots")
		}
		if _, ok := s.floors["home.old64"]; !ok {
			t.Fatal("a floor was shed before older ones")
		}
		if _, ok := s.floors["home.new63"]; !ok {
			t.Fatal("the newest floor was shed")
		}
		return best
	}
	few, full := cost(1<<10), cost(1<<17)
	if full > 8*few+100*time.Microsecond {
		t.Fatalf("a write with %d floors held took %v to prune them, with %d %v", 1<<17, full, 1<<10, few)
	}
}
