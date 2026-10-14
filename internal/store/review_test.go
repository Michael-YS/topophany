package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestExpiredTailCheckpointCrashReplayIsIdempotent(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	o := Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Retention: time.Minute, Now: func() time.Time { return now }}
	s := openTest(t, dir, o)
	v := testkit.Observation(c, "home-http", 1, now, "fail")
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal(e)
	}
	wal, e := os.ReadFile(filepath.Join(dir, "journal.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Minute)
	s.prune()
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if e := os.WriteFile(filepath.Join(dir, "journal.jsonl"), wal, 0600); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, dir, o)
	if n := len(s.tails[tailKey(v)]); n != 1 {
		t.Fatal("expired tail replayed twice", n)
	}
}

func TestStateByteCapacityRejectsBeforeAcknowledgement(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	o := Options{MaxStateBytes: 12 << 10, Now: func() time.Time { return now }}
	s := openTest(t, dir, o)
	v := testkit.Observation(c, "home-http", 1, now, "ok")
	v.Facts["large"] = strings.Repeat("x", 6000)
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal(e)
	}
	v2 := testkit.Observation(c, "home-http", 2, now, "ok")
	v2.Facts["large"] = strings.Repeat("y", 6000)
	if e := s.Put(batch(c, v2)); !errors.Is(e, ErrCapacity) {
		t.Fatal("byte capacity bypass", e)
	}
	if e := s.Checkpoint(); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s = openTest(t, dir, o)
	rows, _ := s.View()
	if len(rows) != 1 || rows[0].ID != v.ID {
		t.Fatal("rejected data persisted or accepted data lost")
	}
}

func TestExpiredReceiptCannotReplaceRetainedEvidence(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Retention: time.Minute, Now: func() time.Time { return now }})
	v := testkit.Observation(c, "home-http", 1, now, "ok")
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Minute)
	if e := s.Put(batch(c, v)); e != nil {
		t.Fatal("identical retained evidence rejected", e)
	}
	v.SampledAt = now
	v.Outcome = "fail"
	if e := s.Put(batch(c, v)); !errors.Is(e, ErrConflict) {
		t.Fatal("expired receipt allowed retained evidence overwrite", e)
	}
}

func TestJournalRecordBudgetDuringRecovery(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "journal.jsonl"), []byte(strings.Repeat("x", (2<<20)+2)), 0600); e != nil {
		t.Fatal(e)
	}
	if s, e := Open(dir, Options{}); e == nil {
		s.Close()
		t.Fatal("oversized recovery record accepted")
	}
}
