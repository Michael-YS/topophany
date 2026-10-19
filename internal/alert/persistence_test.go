package alert

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func TestSlowQueueWriteAllowsReadsAndPersistsNewestState(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var writes atomic.Int32
	n := &Notifier{Path: filepath.Join(t.TempDir(), "queue.json"), queues: map[string]*queue{
		"ops": {Waiting: []queued{{Event: Event{Title: "first"}}, {Event: Event{Title: "second"}}}},
	}}
	n.writeFile = func(path string, raw []byte, mode os.FileMode) error {
		if writes.Add(1) == 1 {
			close(entered)
			<-release
		}
		return secure.AtomicWrite(path, raw, mode)
	}
	tried := make(chan int, 1)
	go func() { tried <- n.tried("ops") }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write did not start")
	}
	read := make(chan int, 1)
	go func() { n.mu.Lock(); tries := n.queues["ops"].Waiting[0].Tries; n.mu.Unlock(); read <- tries }()
	select {
	case tries := <-read:
		if tries != 1 {
			t.Fatal("attempt not visible", tries)
		}
	case <-time.After(time.Second):
		t.Fatal("disk write held queue state mutex")
	}
	finished := make(chan struct{})
	go func() { n.finish("ops", true, ""); close(finished) }()
	wait(t, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return n.queues["ops"].Sent == 1 })
	unblock()
	select {
	case tries := <-tried:
		if tries != 1 {
			t.Fatal("attempt changed during persistence", tries)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attempt did not finish")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("finish did not persist")
	}
	restarted := &Notifier{Path: n.Path}
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	q := restarted.queues["ops"]
	if q.Sent != 1 || len(q.Waiting) != 1 || q.Waiting[0].Event.Title != "second" {
		t.Fatal("an older write replaced newer queue state", q)
	}
}
