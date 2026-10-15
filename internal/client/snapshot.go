package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// errSchema is a snapshot of a schema this client does not read, as from
// a collector of another release.
type errSchema struct{ schema int }

func (e errSchema) Error() string {
	return fmt.Sprintf("a collector serves snapshot schema %d, and this client reads schema %d; upgrade them to the same release", e.schema, model.SnapshotSchema)
}

// decodeSnapshot reads a schema 2 snapshot. Fields it does not know are
// ignored, so a later release can add some without breaking this one;
// everything it knows is checked.
func decodeSnapshot(raw []byte, s *model.Snapshot) error {
	if err := secure.JSONBudget(raw); err != nil {
		return err
	}
	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return err
	}
	if head.Schema != model.SnapshotSchema {
		return errSchema{head.Schema}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := d.Decode(s); err != nil {
		return err
	}
	if err := config.ValidatePublic(s.Topology); err != nil {
		return err
	}
	if !config.ID(s.Instance) || len(s.Revision) != 64 {
		return errors.New("invalid snapshot identity")
	}
	checks := map[string]model.Check{}
	for _, c := range s.Topology.Checks {
		checks[c.ID] = c
	}
	seen := map[string]bool{}
	for _, c := range s.Checks {
		def, ok := checks[c.ID]
		if !ok || seen[c.ID] || c.Observer != def.Observer || (s.ViewObserver != "" && c.Observer != s.ViewObserver) {
			return errors.New("invalid snapshot check")
		}
		seen[c.ID] = true
		if len(c.Contract) != 64 {
			return errors.New("invalid snapshot check contract")
		}
		if v := c.Last; v != nil && (v.Check != c.ID || v.Observer != c.Observer || v.Project != s.Project || v.Contract != c.Contract || v.Revision != "" || !validEvidence(*v)) {
			return errors.New("invalid snapshot observation")
		}
		if len(c.Availability) > 8 {
			return errors.New("invalid snapshot availability")
		}
		for _, a := range c.Availability {
			if a.Window == "" || len(a.Window) > 16 || !validRollup(a.Rollup) {
				return errors.New("invalid snapshot availability")
			}
		}
	}
	for id, def := range checks {
		if (s.ViewObserver == "" || def.Observer == s.ViewObserver) && !seen[id] {
			return errors.New("incomplete snapshot checks")
		}
	}
	validate := func(states []model.EntityState, ids map[string]bool) error {
		if len(states) != len(ids) {
			return errors.New("incomplete snapshot entities")
		}
		seen := map[string]bool{}
		for _, state := range states {
			if !ids[state.ID] || seen[state.ID] {
				return errors.New("invalid snapshot entity")
			}
			seen[state.ID] = true
			refs := map[string]bool{}
			for _, id := range state.Checks {
				if _, ok := checks[id]; !ok || refs[id] {
					return errors.New("invalid snapshot check reference")
				}
				refs[id] = true
			}
		}
		return nil
	}
	nodes, endpoints, services, paths, links, observers := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, v := range s.Topology.Nodes {
		nodes[v.ID] = true
	}
	for _, v := range s.Topology.Endpoints {
		endpoints[v.ID] = true
	}
	for _, v := range s.Topology.Services {
		services[v.ID] = true
	}
	for _, v := range s.Topology.Paths {
		if s.ViewObserver == "" || v.Observer == s.ViewObserver {
			paths[v.ID] = true
		}
	}
	for _, v := range s.Topology.Links {
		links[v.ID] = true
	}
	for _, v := range s.Topology.Observers {
		observers[v.ID] = true
	}
	for _, group := range []struct {
		states []model.EntityState
		ids    map[string]bool
	}{{s.Nodes, nodes}, {s.Endpoints, endpoints}, {s.Services, services}, {s.Paths, paths}, {s.Links, links}} {
		if err := validate(group.states, group.ids); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	if len(s.Observers) != len(observers) {
		return errors.New("incomplete snapshot observers")
	}
	for _, v := range s.Observers {
		if !observers[v.ID] || seen[v.ID] || v.Contracts != "" && v.Contracts != "current" && v.Contracts != "outdated" {
			return errors.New("invalid snapshot observer")
		}
		seen[v.ID] = true
	}
	if s.ViewObserver != "" && !observers[s.ViewObserver] {
		return errors.New("unknown snapshot observer")
	}
	if len(s.Incidents) > len(checks) {
		return errors.New("snapshot incident budget exceeded")
	}
	seen = map[string]bool{}
	for _, v := range s.Incidents {
		if v.ID == "" || seen[v.ID] {
			return errors.New("invalid snapshot incident")
		}
		seen[v.ID] = true
		for _, id := range append(append([]string{}, v.Checks...), v.CounterEvidence...) {
			if _, ok := checks[id]; !ok {
				return errors.New("invalid incident reference")
			}
		}
	}
	// A record may name checks the topology no longer has: they are what
	// failed then. Each incident has at most one record open.
	if len(s.Ledger) > ledger.MaxRecent+2*len(checks)+16 {
		return errors.New("snapshot ledger budget exceeded")
	}
	seen = map[string]bool{}
	open := map[string]bool{}
	for _, r := range s.Ledger {
		key := r.ID + "@" + r.Opened.String()
		if ledger.Valid(r) != nil || seen[key] || r.State != "closed" && open[r.ID] || s.ViewObserver != "" && !strings.HasPrefix(r.Scope, s.ViewObserver+"/") {
			return errors.New("invalid snapshot incident record")
		}
		seen[key] = true
		open[r.ID] = open[r.ID] || r.State != "closed"
	}
	return nil
}

// validEvidence reports whether v is a sample a collector could have
// accepted from a worker, whatever its check.
func validEvidence(v model.Observation) bool {
	return config.ObservationBounds(v, config.MaxTook) == "" && (v.Evidence == "measured" || v.Evidence == "reported")
}

// validRollup reports whether r is a rollup a collector could have sent:
// no time or count below zero, time that adds up to its span, and
// outcomes named in plain text.
func validRollup(r model.Rollup) bool {
	if r.Seconds < 0 || r.Seconds > 1000*24*3600 || r.Samples < 0 || r.Unreconciled < 0 || len(r.Outcomes) > 8 {
		return false
	}
	for _, v := range []int64{r.Up, r.Degraded, r.Down, r.Unknown, r.Unmeasured} {
		if v < 0 {
			return false
		}
	}
	if r.Up+r.Degraded+r.Down+r.Unknown+r.Unmeasured != r.Seconds {
		return false
	}
	counted := int64(0)
	for k, n := range r.Outcomes {
		if k == "" || len(k) > 16 || secure.Text(k) != k || n < 0 {
			return false
		}
		counted += n
	}
	if counted != r.Samples {
		return false
	}
	l := r.Latency
	return l == nil || l.Samples > 0 && l.Samples <= r.Samples && l.P50 >= 0 && l.P50 <= l.P95 && l.P95 <= l.Max
}
