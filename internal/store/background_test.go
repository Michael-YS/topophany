package store

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestBackgroundCheckpointPreservesConcurrentJournalTail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory-sync pause hook is POSIX only")
	}
	c := testkit.Config()
	now := time.Now().UTC()
	options := Options{CheckpointBytes: 1024, MaxJournalBytes: 64 << 10, Now: func() time.Time { return now }, Contracts: config.Contracts(c), Revision: config.Revision(c)}
	s := openTest(t, t.TempDir(), options)
	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	stop := secure.WatchSyncs(func(dir string) {
		if dir == s.dir {
			once.Do(func() { close(paused); <-release })
		}
	})
	defer stop()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	for seq := uint64(1); seq <= 3; seq++ {
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", seq, now, "ok"))); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		running := s.checkpointDone != nil
		s.mu.Unlock()
		if running {
			break
		}
	}
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("background checkpoint did not publish")
	}
	// Both reads and ingestion must complete while checkpoint I/O is paused.
	progressed := make(chan error, 1)
	go func() { s.View(); progressed <- s.Put(batch(c, testkit.Observation(c, "home-http", 10, now, "fail"))) }()
	select {
	case err := <-progressed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("checkpoint held writer lock")
	}
	rows, _ := s.View()
	want := s.Availability("home-http", config.Contracts(c)["home-http"], now)
	// Simulate a crash after checkpoint publication but before WAL replacement.
	crashDir := t.TempDir()
	for _, name := range []string{"checkpoint.json", "journal.jsonl"} {
		raw, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(crashDir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	crash := openTest(t, crashDir, options)
	got, _ := crash.View()
	if len(got) != len(rows) || !reflect.DeepEqual(crash.Availability("home-http", config.Contracts(c)["home-http"], now), want) {
		t.Fatal("old WAL replay lost or double-counted concurrent evidence")
	}
	close(release)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, s.dir, options)
	got, _ = reopened.View()
	if len(got) != len(rows) || !reflect.DeepEqual(reopened.Availability("home-http", config.Contracts(c)["home-http"], now), want) {
		t.Fatal("WAL suffix recovery lost or double-counted evidence")
	}
}

func TestBackgroundCheckpointFailurePoisonsFutureWrites(t *testing.T) {
	c := testkit.Config()
	s := openTest(t, t.TempDir(), Options{CheckpointBytes: 1024, MaxJournalBytes: 64 << 10})
	if err := os.Mkdir(filepath.Join(s.dir, "checkpoint.json"), 0700); err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= 2; seq++ {
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", seq, time.Now(), "ok"))); err != nil {
			break
		}
	}
	s.mu.Lock()
	s.waitCheckpoint()
	poisoned := s.poisoned
	s.mu.Unlock()
	if poisoned == nil {
		t.Fatal("failed background publication was ignored")
	}
	if err := s.Put(batch(c, testkit.Observation(c, "home-http", 3, time.Now(), "ok"))); err == nil {
		t.Fatal("poisoned store accepted data")
	}
	if err := s.Close(); err == nil {
		t.Fatal("Close hid checkpoint failure")
	}
}
