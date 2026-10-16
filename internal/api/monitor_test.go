package api

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func TestMonitorSnapshotDoesNotPersist(t *testing.T) {
	srv, _, batch := fixture(t)
	if e := srv.Store.Put(batch); e != nil {
		t.Fatal(e)
	}
	var syncs atomic.Int64
	stop := secure.WatchSyncs(func(string) { syncs.Add(1) })
	defer stop()
	snap := srv.MonitorSnapshot(time.Now())
	if snap.Project != batch.Project || len(snap.Checks) == 0 {
		t.Fatal(snap)
	}
	if syncs.Load() != 0 {
		t.Fatal("monitor read wrote durable state")
	}
}
