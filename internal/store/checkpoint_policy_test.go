package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestAdaptiveCheckpointThresholdAndRecovery(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	o := Options{CheckpointBytes: 1024, MaxJournalBytes: 64 << 10, Now: func() time.Time { return now }}
	s := openTest(t, dir, o)
	for seq := uint64(1); seq <= 80; seq++ {
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", seq, now, "ok"))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.checkpointSize != st.Size() {
		t.Fatalf("counted %d actual %d", s.checkpointSize, st.Size())
	}
	threshold := s.checkpointThreshold()
	if threshold != min(st.Size(), o.MaxJournalBytes) || threshold <= o.CheckpointBytes {
		t.Fatal("checkpoint did not adapt", threshold, st.Size())
	}
	// Retain an uncheckpointed journal and recover it under the same policy.
	if err := s.Put(batch(c, testkit.Observation(c, "home-http", 81, now, "fail"))); err != nil {
		t.Fatal(err)
	}
	if s.bytes == 0 {
		t.Fatal("large checkpoint rewritten after one small transaction")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, o)
	if s.checkpointThreshold() != threshold || len(s.seen) != 81 {
		t.Fatal("policy or journal recovery changed", s.checkpointThreshold(), len(s.seen))
	}
	s.checkpointSize = 256 << 20
	if s.checkpointThreshold() != o.MaxJournalBytes {
		t.Fatal("replay ceiling ignored")
	}
	s.checkpointSize = 1
	if s.checkpointThreshold() != o.CheckpointBytes {
		t.Fatal("checkpoint floor ignored")
	}
}

func TestCheckpointPolicyLimits(t *testing.T) {
	for _, o := range []Options{{CheckpointBytes: 1024, MaxJournalBytes: 1023}, {MaxJournalBytes: 129 << 20}} {
		if _, err := storageOptions(o); err == nil {
			t.Fatal("unbounded checkpoint policy accepted", o)
		}
	}
	fixed, err := storageOptions(Options{CheckpointBytes: 2048})
	if err != nil || fixed.MaxJournalBytes != 2048 {
		t.Fatal("explicit legacy threshold changed", fixed, err)
	}
	defaults, err := storageOptions(Options{})
	if err != nil || defaults.CheckpointBytes != 16<<20 || defaults.MaxJournalBytes != 128<<20 {
		t.Fatal(defaults, err)
	}
}
