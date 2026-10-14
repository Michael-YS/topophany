package store

import (
	"errors"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestHistoryReservationSurvivesCheckpoint(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	opts := Options{Observers: []string{"home", "cloud"}, MaxHistory: 4, Now: func() time.Time { return now }}
	s := openTest(t, dir, opts)
	if err := s.Put(batch(c, testkit.Observation(c, "cloud-http", 1, now, "ok"))); err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 50; i++ {
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", i, now.Add(time.Duration(i)*time.Millisecond), "ok"))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, dir, opts)
	if got := s.History("cloud-http", config.Revision(c), 100); len(got) != 1 {
		t.Fatal("foreign flood evicted history", len(got))
	}
	if got := s.History("home-http", config.Revision(c), 100); len(got) != 2 {
		t.Fatal("history quota not enforced", len(got))
	}
}

func TestExpiredEvictedIDRemainsBoundAcrossRestart(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	opts := Options{MaxHistory: 2, Retention: time.Minute, Now: func() time.Time { return now }}
	s := openTest(t, dir, opts)
	old := testkit.Observation(c, "home-http", 1, now.Add(-time.Minute), "ok")
	if err := s.Put(batch(c, old)); err != nil {
		t.Fatal(err)
	}
	if !s.seen[old.ID].At.Equal(now) {
		t.Fatal("receipt clock is worker-controlled")
	}
	now = now.Add(2 * time.Minute)
	for i := uint64(2); i <= 45; i++ {
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", i, now, "ok"))); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := s.rowHashes[old.ID]; ok {
		t.Fatal("test failed to evict tail")
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, dir, opts)
	old.Outcome = "fail"
	if err := s.Put(batch(c, old)); !errors.Is(err, ErrConflict) {
		t.Fatal("rebound ID", err)
	}
}

func TestRecoveredHeartbeatContract(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		c := testkit.Config()
		now := time.Now().UTC()
		dir := t.TempDir()
		s := openTest(t, dir, Options{})
		b := batch(c)
		b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now}
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
		if checkpoint {
			if err := s.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
		s.Close()
		s = openTest(t, dir, Options{})
		_, beats := s.StateView()
		for _, change := range []string{"project", "revision", "legacy"} {
			changed := c
			switch change {
			case "project":
				changed.Project = "other"
			case "revision":
				changed.Nodes = append([]model.Node(nil), c.Nodes...)
				changed.Nodes[0].Name = "changed"
			case "legacy":
				h := beats["home"]
				h.Project = ""
				h.Revision = ""
				beats["home"] = h
			}
			snap := engine.Build(changed, "test", "home", nil, beats, now)
			for _, o := range snap.Observers {
				if o.ID == "home" && o.Health == "up" {
					t.Fatal("foreign heartbeat made observer up", checkpoint, change)
				}
			}
		}
		b.Heartbeat.SampledAt = now.Add(time.Second)
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
		_, beats = s.StateView()
		snap := engine.Build(c, "test", "home", nil, beats, now.Add(time.Second))
		for _, o := range snap.Observers {
			if o.ID == "home" && o.Health != "up" {
				t.Fatal("matching beat ignored")
			}
		}
	}
}
