package store

import (
	"os"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestReadersDoNotWaitForJournalSync(t *testing.T) {
	previous := syncFile
	t.Cleanup(func() { syncFile = previous })
	started := make(chan struct{})
	release := make(chan struct{})
	syncFile = func(f *os.File) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return ioaccount.Sync(f)
	}
	c := testkit.Config()
	s := openTest(t, t.TempDir(), Options{Now: func() time.Time { return time.Now().UTC() }, Contracts: config.Contracts(c), Revision: config.Revision(c)})
	done := make(chan error, 1)
	go func() {
		done <- s.Put(batch(c, testkit.Observation(c, "home-http", 1, time.Now().UTC(), "ok")))
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatal("put finished before sync", err)
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not start")
	}
	viewed := make(chan struct{})
	go func() {
		s.View()
		close(viewed)
	}()
	select {
	case <-viewed:
	case <-time.After(time.Second):
		t.Fatal("reader waited for journal sync")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
