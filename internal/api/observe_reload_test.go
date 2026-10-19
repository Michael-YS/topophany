package api

import (
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestObserveAndNotificationShareReloadBoundary(t *testing.T) {
	srv, _, _ := fixture(t)
	srv.Ledger = ledger.New()
	next := testkit.Config()
	next.Checks = next.Checks[1:]
	entered, release, observed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		defer close(observed)
		_, _, err := srv.ObserveWith(srv.Now(), true, func(s model.Snapshot, _ []ledger.Change) {
			if s.Revision != config.Revision(srv.Config) {
				t.Error("notification used another generation")
			}
			// A writer must be excluded from capture through the handoff.
			if srv.reloadMu.TryLock() {
				srv.reloadMu.Unlock()
				t.Error("incident evaluation did not hold reload lock")
			}
			close(entered)
			<-release
		})
		if err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("evaluation did not reach handoff")
	}
	reloaded := make(chan struct{})
	go func() {
		srv.ReloadWith(next, srv.Auth, func() {
			if srv.reloadMu.TryRLock() {
				srv.reloadMu.RUnlock()
				t.Error("notifier reconfiguration was outside reload boundary")
			}
		})
		close(reloaded)
	}()
	select {
	case <-reloaded:
		t.Fatal("reload overtook notification handoff")
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	<-observed
	<-reloaded
	s, _, err := srv.Observe(srv.Now(), true)
	if err != nil || s.Revision != config.Revision(next) {
		t.Fatal("evaluation after reload used obsolete topology", err, s.Revision)
	}
}
