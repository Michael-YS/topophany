package ledger

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func TestSlowLedgerWriteAllowsReadsAndOrdersJudgments(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "incidents.json"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var writes atomic.Int32
	l.writeFile = func(path string, raw []byte, mode os.FileMode) error {
		if writes.Add(1) == 1 {
			close(entered)
			<-release
		}
		return secure.AtomicWrite(path, raw, mode)
	}
	now := time.Now().UTC()
	snap := model.Snapshot{GeneratedAt: now, Incidents: []model.Incident{{ID: "incident", Scope: "home/check", Summary: "first", Checks: []string{"check"}, Since: now}}}
	first := make(chan error, 1)
	go func() { _, err := l.Observe(snap, now, true); first <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write did not start")
	}
	read := make(chan []model.IncidentRecord, 1)
	go func() { read <- l.Recent("", now) }()
	select {
	case records := <-read:
		if len(records) != 1 || records[0].Summary != "first" {
			t.Fatal(records)
		}
	case <-time.After(time.Second):
		t.Fatal("disk write held ledger state mutex")
	}
	secondSnap := snap
	secondSnap.Incidents = append([]model.Incident(nil), snap.Incidents...)
	secondSnap.Incidents[0].Summary = "second"
	second := make(chan error, 1)
	go func() { _, err := l.Observe(secondSnap, now.Add(time.Second), true); second <- err }()
	unblock()
	for _, done := range []chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("judgment did not persist")
		}
	}
	restarted, err := Open(l.path)
	if err != nil {
		t.Fatal(err)
	}
	if records := restarted.Records(); len(records) != 1 || records[0].Summary != "second" {
		t.Fatal("an older judgment replaced the latest", records)
	}
}
