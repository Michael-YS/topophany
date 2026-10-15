// Package ledger remembers incidents: when each opened and when and how it
// ended. A collector keeps one beside its journal and judges it from the
// evidence of all observers, as a snapshot of them would; the records are
// what it concluded, never evidence of their own.
package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Bounds: the records a ledger keeps, how long it keeps a closed one, what
// a snapshot carries of them, and how many checks a record names.
const (
	MaxRecords = 1000
	Retention  = 90 * 24 * time.Hour
	RecentFor  = 24 * time.Hour
	MaxRecent  = 50
	MaxChecks  = 32
	maxBytes   = 4 << 20
)

// How a closed record ended.
const (
	Recovered   = "recovered"
	Unconfirmed = "unconfirmed"
	Paused      = "paused"
	Changed     = "changed"
)

// Change is what happened to a record: it opened, began recovering,
// relapsed while recovering, or closed.
type Change struct {
	Kind   string               `json:"kind"` // opened, recovering, relapsed, closed
	Record model.IncidentRecord `json:"record"`
}

// Ledger is safe for concurrent use. A ledger without a path keeps its
// records in memory only.
type Ledger struct {
	mu        sync.Mutex
	path      string
	records   []model.IncidentRecord // in the order they opened
	unwritten bool                   // the records changed since the last write that succeeded
}

type disk struct {
	Schema  int                    `json:"schema"`
	Records []model.IncidentRecord `json:"records"`
}

// New is a ledger kept in memory.
func New() *Ledger { return &Ledger{} }

// With is a ledger kept in memory that starts with records, in the order
// they opened.
func With(records []model.IncidentRecord) *Ledger {
	l := &Ledger{records: slices.Clone(records)}
	for i := range l.records {
		l.records[i].Checks = slices.Clone(l.records[i].Checks)
	}
	return l
}

// Open reads the ledger at path, or starts one there when there is none.
// An unreadable ledger is refused rather than silently started afresh.
func Open(path string) (*Ledger, error) {
	l := &Ledger{path: path}
	b, e := secure.ReadBounded(path, maxBytes)
	switch {
	case errors.Is(e, os.ErrNotExist):
		return l, nil
	case e != nil:
		return nil, e
	}
	var d disk
	if e = secure.Decode(strings.NewReader(string(b)), &d, maxBytes); e != nil || d.Schema != 1 {
		return nil, fmt.Errorf("incident ledger %s is invalid; move it aside to start a new one", path)
	}
	for _, r := range d.Records {
		if e = valid(r); e != nil {
			return nil, fmt.Errorf("incident ledger %s: %w; move it aside to start a new one", path, e)
		}
	}
	l.records = d.Records
	return l, nil
}

// valid reports whether r is a record a ledger could have written.
func valid(r model.IncidentRecord) error {
	closed := r.State == "closed"
	switch {
	case r.ID == "" || len(r.ID) > 600 || r.Scope == "" || len(r.Scope) > 512 || len(r.Summary) > 512 || len(r.Checks) > MaxChecks || r.Previous < 0:
		return errors.New("invalid incident record")
	case r.State != "open" && r.State != "recovering" && !closed:
		return errors.New("invalid incident record state")
	case r.Opened.IsZero() || r.Since.IsZero() || closed != !r.Closed.IsZero() || closed != (r.Ended != ""):
		return errors.New("invalid incident record times")
	case closed && r.Ended != Recovered && r.Ended != Unconfirmed && r.Ended != Paused && r.Ended != Changed:
		return errors.New("invalid incident record ending")
	}
	for _, id := range r.Checks {
		if !config.ID(id) {
			return errors.New("invalid incident record check")
		}
	}
	return nil
}

// Valid reports whether r is a record a ledger could have written.
func Valid(r model.IncidentRecord) error { return valid(r) }

// Observe records what snap, a snapshot of all observers, says at now: an
// incident not yet recorded opens, and a record whose incident is gone
// recovers or closes. Until settled, a record whose evidence lapsed stays
// open: a collector that just started has not yet heard from its workers.
// It returns what changed; an error says only that the ledger could not
// be written, and the records are kept in memory regardless and written
// at the next call, whether or not anything changes by then, so that no
// error means they are on disk.
func (l *Ledger) Observe(snap model.Snapshot, now time.Time, settled bool) ([]Change, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	changes := []Change{}
	dirty := false
	open := map[string]int{}
	for i, r := range l.records {
		if r.State != "closed" {
			open[r.ID] = i
		}
	}
	current, scopes := map[string]bool{}, map[string]string{}
	for _, inc := range snap.Incidents {
		current[inc.ID], scopes[inc.Scope] = true, inc.ID
		checks := slices.Clone(inc.Checks[:min(len(inc.Checks), MaxChecks)])
		summary := model.SummaryText(inc.Summary)
		since := inc.Since
		if since.IsZero() {
			since = now
		}
		i, ok := open[inc.ID]
		if !ok {
			previous := 0
			for _, r := range l.records {
				if r.ID == inc.ID {
					previous++
				}
			}
			r := model.IncidentRecord{ID: inc.ID, Scope: inc.Scope, Summary: summary, Checks: checks, State: "open", Since: since, Opened: now, Previous: previous}
			l.records = append(l.records, r)
			open[inc.ID] = len(l.records) - 1
			changes = append(changes, Change{"opened", r})
			dirty = true
			continue
		}
		r := &l.records[i]
		if r.State == "recovering" {
			r.State = "open"
			changes = append(changes, Change{"relapsed", *r})
		}
		if r.Summary != summary || !slices.Equal(r.Checks, checks) {
			r.Summary, r.Checks = summary, checks
			dirty = true
		}
		if since.Before(r.Since) {
			r.Since = since
			dirty = true
		}
	}
	states := map[string]model.CheckState{}
	for _, c := range snap.Checks {
		states[c.ID] = c
	}
	for i := range l.records {
		r := &l.records[i]
		if r.State == "closed" || current[r.ID] {
			continue
		}
		ended, recovering := judge(*r, states, scopes)
		switch {
		case recovering:
			if r.State != "recovering" {
				r.State = "recovering"
				changes = append(changes, Change{"recovering", *r})
			}
		case ended == Unconfirmed && !settled:
		default:
			r.State, r.Closed, r.Ended = "closed", now, ended
			changes = append(changes, Change{"closed", *r})
		}
	}
	dirty = dirty || len(changes) > 0 || l.unwritten
	if l.prune(now) {
		dirty = true
	}
	if !dirty || l.path == "" {
		return changes, nil
	}
	e := l.write()
	l.unwritten = e != nil
	return changes, e
}

// judge says how a record whose incident is gone ended, or that it is
// recovering: its checks pass again, short of their recovery threshold.
func judge(r model.IncidentRecord, states map[string]model.CheckState, scopes map[string]string) (ended string, recovering bool) {
	if id, ok := scopes[r.Scope]; ok && id != r.ID {
		return Changed, false
	}
	up, paused := true, false
	for _, id := range r.Checks {
		s, ok := states[id]
		switch {
		case !ok:
			return Changed, false
		case s.Freshness == "paused" || s.Freshness == "maintenance":
			paused, up = true, false
		case s.Freshness == "fresh" && s.Health == "degraded" && s.Last != nil && s.Last.Outcome == "ok":
			return "", true
		case s.Freshness != "fresh" || s.Health != "up":
			up = false
		}
	}
	switch {
	case up:
		return Recovered, false
	case paused:
		return Paused, false
	}
	return Unconfirmed, false
}

// prune drops closed records past the retention, then the oldest closed
// ones beyond the bound, and reports whether it dropped any. Open records
// are never dropped.
func (l *Ledger) prune(now time.Time) bool {
	n := len(l.records)
	l.records = slices.DeleteFunc(l.records, func(r model.IncidentRecord) bool {
		return r.State == "closed" && now.Sub(r.Closed) > Retention
	})
	over := len(l.records) - MaxRecords
	l.records = slices.DeleteFunc(l.records, func(r model.IncidentRecord) bool {
		if over > 0 && r.State == "closed" {
			over--
			return true
		}
		return false
	})
	return len(l.records) != n
}

func (l *Ledger) write() error {
	b, e := json.Marshal(disk{Schema: 1, Records: l.records})
	if e != nil {
		return e
	}
	return secure.AtomicWrite(l.path, b, 0600)
}

// Recent is what a snapshot of view carries at now: the records open and
// recovering, then at most MaxRecent closed in the last RecentFor, newest
// first. A view of one observer has only the records of its scopes.
func (l *Ledger) Recent(view string, now time.Time) []model.IncidentRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	open, closed := []model.IncidentRecord{}, []model.IncidentRecord{}
	for i := len(l.records) - 1; i >= 0; i-- {
		r := l.records[i]
		switch {
		case view != "" && !strings.HasPrefix(r.Scope, view+"/"):
		case r.State != "closed":
			open = append(open, r)
		case now.Sub(r.Closed) <= RecentFor && len(closed) < MaxRecent:
			closed = append(closed, r)
		}
	}
	out := append(open, closed...)
	for i := range out {
		out[i].Checks = slices.Clone(out[i].Checks)
	}
	return out
}

// Records is every record the ledger holds, in the order they opened.
func (l *Ledger) Records() []model.IncidentRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := slices.Clone(l.records)
	for i := range out {
		out[i].Checks = slices.Clone(out[i].Checks)
	}
	return out
}

// Settle is how long a collector that just started waits before it closes
// a record whose evidence lapsed: as long as its stalest check takes to
// lapse, between ten seconds and fifteen minutes, so its workers can
// deliver what they hold.
func Settle(c model.Config) time.Duration {
	longest := 10 * time.Second
	for _, x := range c.Checks {
		longest = max(longest, time.Duration(x.StaleSeconds)*time.Second)
	}
	return min(longest, 15*time.Minute)
}

// Line is a change as a collector logs it: "incident opened: home/path/p
// (3f2a9c1b04de/home/path/p); checks: a, b".
func (c Change) Line() string {
	r := c.Record
	line := "incident " + c.Kind + ": " + r.Scope + " (" + r.ID + ")"
	switch c.Kind {
	case "opened":
		line += "; checks: " + strings.Join(r.Checks, ", ")
	case "closed":
		line += ", " + r.Ended + " after " + r.Closed.Sub(r.Since).Round(time.Second).String()
	}
	return secure.Text(line)
}
