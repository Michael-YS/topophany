package logging

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

type repetition struct {
	component, entity, code string
	count                   uint64
	first, last, emitted    time.Time
}

// Repeater groups sanitized error codes by component/entity. Counts refer to
// repeated reports, not network attempts. Recovery requires a healthy report.
type Repeater struct {
	mu      sync.Mutex
	Out     io.Writer
	Every   time.Duration
	entries map[string]repetition
}

func (r *Repeater) emit(event string, v repetition, now time.Time) {
	fmt.Fprintf(r.Out, "at=%s event=%s component=%s entity=%s code=%s reports=%d first_at=%s last_at=%s\n", now.UTC().Format(time.RFC3339), event, v.component, v.entity, v.code, v.count, v.first.UTC().Format(time.RFC3339), v.last.UTC().Format(time.RFC3339))
}
func bounded(s string) string {
	s = secure.Text(s)
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}
func (r *Repeater) Update(component, entity, code string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Out == nil {
		return
	}
	if r.entries == nil {
		r.entries = map[string]repetition{}
	}
	component, entity, code = bounded(component), bounded(entity), bounded(code)
	key := component + "\x00" + entity
	v, ok := r.entries[key]
	if code == "" {
		if ok {
			r.emit("recovered", v, now)
			delete(r.entries, key)
		}
		return
	}
	if ok && v.code != code {
		r.emit("changed", v, now)
		delete(r.entries, key)
		ok = false
	}
	if !ok {
		if len(r.entries) >= 128 {
			var oldest string
			var at time.Time
			for k, x := range r.entries {
				if oldest == "" || x.last.Before(at) {
					oldest = k
					at = x.last
				}
			}
			r.emit("evicted", r.entries[oldest], now)
			delete(r.entries, oldest)
		}
		v = repetition{component: component, entity: entity, code: code, first: now, last: now, emitted: now, count: 1}
		r.emit("error", v, now)
	} else {
		v.last = now
		v.count++
		every := r.Every
		if every <= 0 {
			every = time.Minute
		}
		if now.Sub(v.emitted) >= every {
			r.emit("repeated", v, now)
			v.emitted = now
		}
	}
	r.entries[key] = v
}
