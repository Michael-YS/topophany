package probe

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/runner"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func hostWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("host operation did not finish")
		var zero T
		return zero
	}
}

func TestDiskTimeoutsBoundOutstandingCallsAndRecover(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan string, maxDiskCalls)
	var reads atomic.Int32
	s := &diskSampler{read: func(path string) (HostDisk, error) {
		reads.Add(1)
		if path == "/healthy" {
			return hostFixture().Disk, nil
		}
		started <- path
		<-release
		return HostDisk{}, hostReadError("disk")
	}}
	var calls []*diskCall
	for i := 0; i < maxDiskCalls; i++ {
		path := fmt.Sprintf("/blocked-%d", i)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := s.sample(ctx, path); done <- err }()
		if hostWait(t, started) != path {
			t.Fatal("wrong filesystem")
		}
		cancel()
		if err := hostWait(t, done); err == nil {
			t.Fatal("canceled call succeeded")
		}
		s.mu.Lock()
		calls = append(calls, s.pending[path])
		s.mu.Unlock()
	}
	// Repeated deadlines on a stalled path must reuse its outstanding syscall.
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		done := make(chan error, 1)
		go func() { _, err := s.sample(ctx, "/blocked-0"); done <- err }()
		err := hostWait(t, done)
		cancel()
		_, _, code := classify(err)
		if code != "host_sampling_canceled" {
			t.Fatal(err)
		}
	}
	_, err := s.sample(context.Background(), "/healthy")
	_, _, code := classify(err)
	if code != "host_disk_busy" || reads.Load() != maxDiskCalls {
		t.Fatal("unbounded filesystem work", err, reads.Load())
	}
	// Release the actual syscalls, then reuse the slots. Errors are not cached.
	for range maxDiskCalls {
		release <- struct{}{}
	}
	for _, call := range calls {
		hostWait(t, call.done)
	}
	if disk, err := s.sample(context.Background(), "/healthy"); err != nil || disk != hostFixture().Disk {
		t.Fatal("disk sampler did not recover", disk, err)
	}
}

func TestBlockedDiskDoesNotPreventSchedulerStopOrReplacement(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 1)
	disks := &diskSampler{read: func(path string) (HostDisk, error) {
		if path == "/blocked" {
			started <- struct{}{}
			<-release
		}
		return hostFixture().Disk, nil
	}}
	c := testkit.Config()
	c.Checks = []model.Check{{ID: "host", Observer: "home", Kind: "host", Host: &model.HostOptions{DiskPath: "/blocked"}}}
	config.Defaults(&c)
	newScheduler := func() *runner.Scheduler {
		collector := newHostCollector(nil)
		collector.read = func(context.Context, []string) (hostRuntime, error) {
			return hostRuntime{HostSample: hostFixture()}, nil
		}
		collector.disk = disks.sample
		s, err := runner.New(c, "home", 1, Runner{HostSample: collector.sample}.Run, func(o model.Observation) error {
			if o.Outcome != "ok" {
				return fmt.Errorf("unexpected replacement sample: %s", o.Code)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := newScheduler()
	done := make(chan error, 1)
	go func() { done <- old.Run(ctx) }()
	hostWait(t, started)
	cancel()
	if err := hostWait(t, done); err != nil {
		t.Fatal("shutdown blocked", err)
	}
	// A reloaded topology can probe a healthy mount while the old call is stuck.
	c.Checks[0].Host.DiskPath = "/healthy"
	next := newScheduler()
	go func() { done <- next.Once(context.Background()) }()
	if err := hostWait(t, done); err != nil {
		t.Fatal("replacement scheduler blocked", err)
	}
}
