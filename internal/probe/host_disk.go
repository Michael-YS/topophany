package probe

import (
	"context"
	"sync"
)

// A stalled filesystem syscall cannot be canceled in Go. Isolate it from the
// scheduler and bound outstanding calls across all runners and topology reloads.
// Repeated requests for a stalled path join the same call instead of leaking
// goroutines. Slots remain occupied until the actual syscall returns.
const maxDiskCalls = 4

var hostDisks = diskSampler{read: readDisk}

type diskCall struct {
	done chan struct{}
	disk HostDisk
	err  error
}

type diskSampler struct {
	mu      sync.Mutex
	pending map[string]*diskCall
	read    func(string) (HostDisk, error)
}

func sampleDisk(ctx context.Context, path string) (HostDisk, error) {
	return hostDisks.sample(ctx, path)
}

func (s *diskSampler) sample(ctx context.Context, path string) (HostDisk, error) {
	if ctx.Err() != nil {
		return HostDisk{}, local("host", "host_sampling_canceled")
	}
	s.mu.Lock()
	call := s.pending[path]
	if call == nil {
		if len(s.pending) >= maxDiskCalls {
			s.mu.Unlock()
			return HostDisk{}, local("host", "host_disk_busy")
		}
		if s.pending == nil {
			s.pending = make(map[string]*diskCall)
		}
		call = &diskCall{done: make(chan struct{})}
		s.pending[path] = call
		go func() {
			call.disk, call.err = s.read(path)
			s.mu.Lock()
			delete(s.pending, path)
			close(call.done)
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return HostDisk{}, local("host", "host_sampling_canceled")
	case <-call.done:
		if ctx.Err() != nil {
			return HostDisk{}, local("host", "host_sampling_canceled")
		}
		return call.disk, call.err
	}
}
