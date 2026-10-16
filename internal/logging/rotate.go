// Package logging provides bounded local logs and repeated error summaries.
package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Rotating struct {
	mu      sync.Mutex
	path    string
	max     int64
	backups int
	file    *os.File
	size    int64
	unlock  func() error
	closed  bool
}

func Open(path string, maxBytes int64, backups int) (*Rotating, error) {
	if maxBytes < 1 || backups < 1 || backups > 10 {
		return nil, errors.New("invalid log rotation limits")
	}
	if filepath.Ext(path) != ".log" {
		return nil, errors.New("managed log path must end in .log")
	}
	path, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	path = filepath.Join(parent, filepath.Base(path))
	unlock, e := secure.Lock(path + ".lock")
	if e != nil {
		return nil, e
	}
	r := &Rotating{path: path, max: maxBytes, backups: backups, unlock: unlock}
	for i := 0; i <= backups; i++ {
		p := path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", path, i)
		}
		if e = regular(p); e != nil {
			unlock()
			return nil, e
		}
	}
	if e = r.open(); e != nil {
		unlock()
		return nil, e
	}
	return r, nil
}
func regular(path string) error {
	st, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("log must be a regular non-symlink file")
	}
	return nil
}
func (r *Rotating) open() error {
	if e := regular(r.path); e != nil {
		return e
	}
	f, e := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return e
	}
	r.file = f
	r.size = st.Size()
	return nil
}
func (r *Rotating) rotate() error {
	if r.file != nil {
		if e := r.file.Close(); e != nil {
			return e
		}
		r.file = nil
	}
	for i := r.backups; i >= 1; i-- {
		to := fmt.Sprintf("%s.%d", r.path, i)
		from := r.path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", r.path, i-1)
		}
		if e := regular(to); e != nil {
			return e
		}
		if e := regular(from); e != nil {
			return e
		}
		if i == r.backups {
			if e := os.Remove(to); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
		if e := os.Rename(from, to); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return r.open()
}
func (r *Rotating) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, os.ErrClosed
	}
	if r.file == nil {
		if e := r.open(); e != nil {
			return 0, e
		}
	}
	total := 0
	for len(b) > 0 {
		if r.size >= r.max {
			if e := r.rotate(); e != nil {
				return total, e
			}
		}
		n, e := r.file.Write(b[:min(int64(len(b)), r.max-r.size)])
		total += n
		r.size += int64(n)
		b = b[n:]
		if e != nil {
			return total, e
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}
func (r *Rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var e error
	if r.file != nil {
		e = r.file.Close()
	}
	return errors.Join(e, r.unlock())
}
