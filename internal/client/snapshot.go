package client

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func decodeSnapshot(raw []byte, s *model.Snapshot) error {
	if err := secure.JSONBudget(raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
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
		if v := c.Last; v != nil && (v.Check != c.ID || v.Observer != c.Observer || v.Project != s.Project || v.Revision != s.Revision || len(v.Facts) > 32 || len(v.Timings) > 16) {
			return errors.New("invalid snapshot observation")
		}
	}
	validate := func(states []model.EntityState, ids map[string]bool) error {
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
	nodes, services, paths, links, observers := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, v := range s.Topology.Nodes {
		nodes[v.ID] = true
	}
	for _, v := range s.Topology.Services {
		services[v.ID] = true
	}
	for _, v := range s.Topology.Paths {
		paths[v.ID] = true
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
	}{{s.Nodes, nodes}, {s.Services, services}, {s.Paths, paths}, {s.Links, links}} {
		if err := validate(group.states, group.ids); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, v := range s.Observers {
		if !observers[v.ID] || seen[v.ID] {
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
	return nil
}
