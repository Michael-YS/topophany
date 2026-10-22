package stream

import (
	"context"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/monitor"
)

// DirectManager owns at most an installed acquisition session and a candidate.
// Projection handles borrow it; closing an old projection cannot close the
// acquisition that its replacement has already borrowed.
type DirectManager struct {
	mu      sync.Mutex
	root    string
	now     func() time.Time
	current *direct.Session
	refs    map[*direct.Session]int
	closed  bool
}

func NewDirectManager(root string, now func() time.Time) *DirectManager {
	if now == nil {
		now = time.Now
	}
	return &DirectManager{root: root, now: now, refs: map[*direct.Session]int{}}
}
func (m *DirectManager) Close() {
	m.mu.Lock()
	m.closed = true
	all := []*direct.Session{}
	for s := range m.refs {
		all = append(all, s)
	}
	m.refs = map[*direct.Session]int{}
	m.current = nil
	m.mu.Unlock()
	for _, s := range all {
		s.Close()
	}
}
func (m *DirectManager) Open(ctx context.Context, r Request) (Source, *Error) {
	if r.Source != "direct" {
		return openSource(ctx, r, m.now)
	}
	st, e := direct.NewStore(m.root)
	if e != nil {
		return Source{}, directFailure(e)
	}
	p, e := st.Load(r.Profile)
	if e != nil {
		return Source{}, directFailure(e)
	}
	if r.ProfileRevision != "" && r.ProfileRevision != p.Revision {
		return Source{}, failure("profile_conflict", "Direct profile changed; list profiles before retrying.")
	}
	if r.Project != "" && r.Project != p.Project {
		return Source{}, failure("project_mismatch", "Direct profile project differs from request.")
	}
	if r.Observer != "" && r.Observer != direct.Observer {
		return Source{}, failure("invalid_request", "Unknown direct observer.")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Source{}, failure("profile_unavailable", "Direct reader is closed.")
	}
	s := m.current
	fresh := s == nil || s.Profile().Profile != p.Profile
	if fresh {
		if len(m.refs) >= 2 {
			return Source{}, failure("state_budget", "Direct candidate session limit reached.")
		}
		s, e = direct.NewSession(ctx, st, p, direct.Options{Now: m.now})
		if e != nil {
			return Source{}, directFailure(e)
		}
	} else if e = s.CheckRetry(r.RetryNonce); e != nil {
		return Source{}, directFailure(e)
	} else if e = s.UpdateProjection(p); e != nil {
		return Source{}, directFailure(e)
	}
	m.refs[s]++
	m.current = s
	if fresh {
		s.RememberNonce(r.RetryNonce)
	} else {
		s.Retry(r.RetryNonce)
	}
	fetch := func(ctx context.Context) (monitor.Input, error) {
		if e := ctx.Err(); e != nil {
			return monitor.Input{}, e
		}
		snap, e := s.Snapshot(r.Observer, m.now())
		return monitor.Input{Snapshot: snap}, e
	}
	initial, _ := fetch(ctx)
	if fresh {
		initial.Snapshot = direct.LoadingSnapshot(p, r.Observer, m.now())
	}
	var once sync.Once
	closeHandle := func() {
		once.Do(func() {
			m.mu.Lock()
			m.refs[s]--
			closeSession := m.refs[s] <= 0
			if closeSession {
				delete(m.refs, s)
				if m.current == s {
					m.current = nil
				}
			}
			m.mu.Unlock()
			if closeSession {
				s.Close()
			}
		})
	}
	return Source{Project: p.Project, Initial: initial, Fetch: fetch, Close: closeHandle}, nil
}
func directFailure(err error) *Error { e := direct.AsError(err); return failure(e.Code, e.Message) }
