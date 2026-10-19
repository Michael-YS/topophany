package store

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestHistoryDetachesEvidence(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{})
	v := testkit.Observation(c, "home-http", 1, now, "ok")
	v.Timings = map[string]int64{"connect": 12}
	if err := s.Put(batch(c, v)); err != nil {
		t.Fatal(err)
	}
	history := s.History("home-http", config.Contracts(c), config.Revision(c), 1)
	history[0].Facts["audit"] = "changed through reader"
	history[0].Timings["connect"] = 999
	if s.rows[0].Facts["audit"] != "" || s.rows[0].Timings["connect"] != 12 {
		t.Fatal("History result aliases stored Facts; mutation reached persisted evidence")
	}
}

func TestConcurrentHistoryReaders(t *testing.T) {
	c := testkit.Config()
	s := openTest(t, t.TempDir(), Options{})
	if err := s.Put(batch(c, testkit.Observation(c, "home-http", 1, time.Now().UTC(), "ok"))); err != nil {
		t.Fatal(err)
	}
	contracts, rev := config.Contracts(c), config.Revision(c)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			<-start
			for j := 0; j < 20; j++ {
				rows := s.History("home-http", contracts, rev, 1)
				rows[0].Facts["reader"] = "owned"
				s.StateView()
			}
		})
	}
	close(start)
	wg.Wait()
}

func TestTailReleasesDiscardedBacking(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{})
	values := make([]model.Observation, 20000)
	for i := range values {
		values[i] = testkit.Observation(c, "home-http", uint64(i+1), now.Add(time.Duration(i)*time.Second), "ok")
	}
	tail := s.trim(tailKey(values[0]), values)
	if len(tail) != tailLength {
		t.Fatal(len(tail))
	}
	if &tail[0] == &values[len(values)-tailLength] && values[0].Facts != nil {
		t.Fatalf("%d-row tail pins all %d rows and dropped Facts", len(tail), len(values))
	}
}

func BenchmarkSteadyHistoryPruning(b *testing.B) {
	for _, n := range []int{1000, 20000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			c := testkit.Config()
			now := time.Now().UTC()
			s, err := Open(b.TempDir(), Options{MaxHistory: n, Now: func() time.Time { return now }})
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			for i := 0; i < n; i++ {
				s.addRow(testkit.Observation(c, "home-http", uint64(i+1), now.Add(time.Duration(i-n)*time.Millisecond), "ok"))
			}
			s.prune()
			v := testkit.Observation(c, "home-http", uint64(n+1), now, "ok")
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				v.Sequence++
				v.ID = fmt.Sprintf("home.audit.%d", v.Sequence)
				s.addRow(v)
				s.prune()
			}
		})
	}
}

func TestReloadAndRestartReleaseExpiredHistory(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Now: func() time.Time { return now }})
	for i := 0; i < 2000; i++ {
		v := testkit.Observation(c, "home-http", uint64(i+1), now.Add(time.Duration(i-2000)*time.Second), "ok")
		v.Facts["payload1"] = fmt.Sprintf("%04d:%s", i, strings.Repeat("x", 2043))
		v.Facts["payload2"] = fmt.Sprintf("%04d:%s", i, strings.Repeat("y", 2043))
		if code := config.ObservationBounds(v, time.Second); code != "" {
			t.Fatal(code)
		}
		s.addRow(v)
	}
	s.SetCurrent(config.Revision(c), config.Contracts(c), config.Stale(c))
	now = now.Add(48 * time.Hour)
	s.prune()
	check := func() {
		t.Helper()
		if len(s.rows) != 0 || len(s.rowSizes) != tailLength {
			t.Fatalf("history=%d indexed=%d", len(s.rows), len(s.rowSizes))
		}
		for _, tail := range s.tails {
			if len(tail) != tailLength || cap(tail) > 2*tailLength {
				t.Fatal("tail retains oversized allocation", len(tail), cap(tail))
			}
		}
		if s.rowsCapacity != 0 {
			t.Fatal("empty history retains backing allocation")
		}
		checkAccounting(t, s)
	}
	check()
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	dir, options := s.dir, s.options
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, options)
	check()
}
