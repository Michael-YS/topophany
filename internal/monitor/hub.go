package monitor

import (
	"errors"
	"sync"
)

// Hub owns snapshots and sends the latest one to each bounded subscriber. Slow
// consumers skip frames. Subscribe includes the current frame under the same
// lock, so fetching and subscribing cannot lose an intervening update.
type Hub struct {
	mu     sync.Mutex
	latest Panel
	ready  bool
	subs   map[chan Panel]bool
}

func (h *Hub) Publish(p Panel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.latest = clone(p)
	h.ready = true
	for ch := range h.subs {
		select {
		case <-ch:
		default:
		}
		ch <- clone(p)
	}
}
func (h *Hub) Latest() (Panel, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return clone(h.latest), h.ready
}
func (h *Hub) Subscribe() (<-chan Panel, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= 64 {
		return nil, nil, errors.New("monitor subscriber limit reached")
	}
	if h.subs == nil {
		h.subs = map[chan Panel]bool{}
	}
	ch := make(chan Panel, 1)
	h.subs[ch] = true
	if h.ready {
		ch <- clone(h.latest)
	}
	var once sync.Once
	return ch, func() { once.Do(func() { h.mu.Lock(); delete(h.subs, ch); close(ch); h.mu.Unlock() }) }, nil
}
