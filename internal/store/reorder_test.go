package store

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestLateRollupCorrectionSurvivesJournalAndCheckpoint(t *testing.T) {
	c := testkit.Config()
	start := time.Now().UTC().Truncate(5 * time.Minute)
	now := start.Add(3 * time.Minute)
	o := Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Now: func() time.Time { return now }}
	dir := t.TempDir()
	s := openTest(t, dir, o)
	for _, seq := range []uint64{1, 3} {
		v := testkit.Observation(c, "home-http", seq, start.Add(time.Duration(seq-1)*time.Minute), "ok")
		if e := s.Put(batch(c, v)); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s = openTest(t, dir, o)
	late := batch(c, testkit.Observation(c, "home-http", 2, start.Add(time.Minute), "fail"))
	if e := s.Put(late); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		got, _, _ := s.Rollups("home-http", config.Contracts(c)["home-http"], start, 5*time.Minute, now)
		if got.Up != 120 || got.Down != 60 || got.Samples != 3 || got.Unreconciled != 0 {
			t.Fatal(i, got)
		}
		if e := s.Put(late); e != nil {
			t.Fatal(e)
		} // retries must not correct or count twice
		if i == 1 {
			if e := s.Checkpoint(); e != nil {
				t.Fatal(e)
			}
		}
		s.Close()
		s = openTest(t, dir, o)
	}
}
