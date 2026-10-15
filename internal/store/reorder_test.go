package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestReverseLateJournalSuffixHasBoundedCorrections(t *testing.T) {
	c := testkit.Config()
	start := time.Now().UTC().Truncate(time.Hour)
	now := start.Add(time.Minute)
	opts := Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Now: func() time.Time { return now }}
	dir := t.TempDir()
	s := openTest(t, dir, opts)
	for i := 0; i < 20; i++ {
		b := batch(c)
		for j := 0; j < 100; j++ {
			seq := uint64(i*100 + j + 1)
			b.Observations = append(b.Observations, testkit.Observation(c, "home-http", seq, start.Add(time.Duration(2*seq)*time.Millisecond), "ok"))
		}
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Simulate interruption after fsync but before applying the late batch.
	late := batch(c)
	for i := 99; i >= 0; i-- {
		v := testkit.Observation(c, "home-http", uint64(2100-i), start.Add(time.Duration(2*i+1)*time.Millisecond), "fail")
		v.ReceivedAt = now
		late.Observations = append(late.Observations, v)
	}
	record, err := wrap(late)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.jsonl"), append(record, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s = openTest(t, dir, opts)
		got, _, _ := s.Rollups("home-http", config.Contracts(c)["home-http"], start, 5*time.Minute, now)
		if got.Samples != 2100 || got.Unreconciled != 100 {
			t.Fatal("replay changed bounded correction counts", i, got)
		}
		// Another observer can still store and read its own sample.
		if err := s.Put(batch(c, testkit.Observation(c, "cloud-http", uint64(i+1), now, "ok"))); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err := s.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
		s.Close()
	}
}

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
