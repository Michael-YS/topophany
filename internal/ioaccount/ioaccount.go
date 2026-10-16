// Package ioaccount measures application I/O, not physical device writes.
// Counters are bounded, process-local, monotonic, and never persisted.
package ioaccount

import (
	"os"
	"sync/atomic"
	"time"
)

type Counter struct{ Count, Bytes, Errors, Nanoseconds atomic.Uint64 }

func (c *Counter) Add(bytes int, elapsed time.Duration, err error) {
	c.Count.Add(1)
	if bytes > 0 {
		c.Bytes.Add(uint64(bytes))
	}
	c.Nanoseconds.Add(uint64(max(elapsed, 0)))
	if err != nil {
		c.Errors.Add(1)
	}
}

type Measurement struct {
	Count       uint64 `json:"count"`
	Bytes       uint64 `json:"bytes"`
	Errors      uint64 `json:"errors"`
	Nanoseconds uint64 `json:"nanoseconds"`
}

func (c *Counter) Read() Measurement {
	return Measurement{c.Count.Load(), c.Bytes.Load(), c.Errors.Load(), c.Nanoseconds.Load()}
}

var Journal, Checkpoint, Queue, FileSync, DirectorySync Counter

type Snapshot struct {
	Journal       Measurement `json:"journal"`
	Checkpoint    Measurement `json:"checkpoint"`
	Queue         Measurement `json:"queue"`
	FileSync      Measurement `json:"file_sync"`
	DirectorySync Measurement `json:"directory_sync"`
}

func Read() Snapshot {
	return Snapshot{Journal.Read(), Checkpoint.Read(), Queue.Read(), FileSync.Read(), DirectorySync.Read()}
}
func Sync(f *os.File) error {
	start := time.Now()
	err := f.Sync()
	FileSync.Add(0, time.Since(start), err)
	return err
}
