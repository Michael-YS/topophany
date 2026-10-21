package store

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

// maxExternalStateBytes is the independent persisted budget for report
// cursors, contact state and bounded confirmation statistics.
const maxExternalStateBytes = 4 << 20

// SetExternalContext installs the reviewed bindings and sources a store may
// admit. It is called by the API for both startup and reload, and before a
// local collector begins writing external observations. State for a binding
// whose contract changed is removed instead of reused.
func (s *Store) SetExternalContext(c model.Config) {
	bindings := make(map[string]model.ExternalBinding)
	sources := make(map[string]model.Source)
	for _, source := range c.Sources {
		sources[source.ID] = source
	}
	for _, x := range c.Checks {
		if x.Kind == "external" && x.External != nil {
			bindings[x.ID] = *x.External
		}
	}
	contracts := config.Contracts(c)
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.Bindings, s.options.Sources = bindings, sources
	s.pruneExternalLocked(contracts)
}

// ExternalView returns a detached copy of persisted external acceptance
// state for tests and projections.
func (s *Store) ExternalView() *model.ExternalState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return model.CloneExternalState(s.external)
}

// pruneExternalLocked removes binding state whose contract is no longer
// current, then source state no remaining binding references.
func (s *Store) pruneExternalLocked(contracts map[string]string) {
	if s.external == nil {
		return
	}
	for check, state := range s.external.Bindings {
		if contracts[check] == "" || contracts[check] != state.Contract {
			delete(s.external.Bindings, check)
		}
	}
	used := map[string]bool{}
	for _, state := range s.external.Bindings {
		used[state.Source] = true
	}
	for source := range s.external.Sources {
		if !used[source] {
			delete(s.external.Sources, source)
		}
	}
	s.resizeExternalLocked()
}

// resizeExternalLocked recomputes serialized state bytes and collapses an
// empty state to nil. Callers hold s.mu.
func (s *Store) resizeExternalLocked() {
	if len(s.external.Sources) == 0 && len(s.external.Bindings) == 0 {
		s.external = nil
		size := int64(0)
		s.stateBytes += size - s.externalBytes
		s.externalBytes = size
		return
	}
	raw, err := json.Marshal(s.external)
	if err != nil {
		// State came from validated values; a marshal failure means the
		// store cannot safely continue.
		s.poisoned = fmt.Errorf("external state encoding failed: %w", err)
		return
	}
	size := int64(len(raw))
	s.stateBytes += size - s.externalBytes
	s.externalBytes = size
}

// admitExternalLocked normalizes and validates one external observation
// against the installed context. It is also the direct-local-collector
// admission path, which does not pass through the API. Callers hold
// commitMu and s.mu.
func (s *Store) admitExternalLocked(v *model.Observation) error {
	if v.External == nil {
		if _, ok := s.options.Bindings[v.Check]; ok {
			return errors.New("external check requires a collection attempt")
		}
		return nil
	}
	binding, ok := s.options.Bindings[v.Check]
	if !ok {
		return errors.New("external observation has no declared binding")
	}
	source, ok := s.options.Sources[binding.Source]
	if !ok {
		return errors.New("external observation has no declared source")
	}
	x := model.Check{ID: v.Check, Observer: source.Observer, Kind: "external", External: &binding}
	normalized, err := report.ValidateExternalObservation(*v, x, source)
	if err != nil {
		return fmt.Errorf("external observation: %w", err)
	}
	v.External.Report = normalized
	return nil
}

// mergeExternalLocked derives the next external state from the observations
// this transaction will commit. It does not mutate the live state: Put
// installs the result only after the journal is durable.
func (s *Store) mergeExternalLocked(observations []model.Observation) (*model.ExternalState, int64, error) {
	next := model.CloneExternalState(s.external)
	if next == nil {
		next = model.NewExternalState()
	}
	for _, v := range observations {
		if v.External == nil || !s.current(v) {
			continue
		}
		source := v.External.Source
		sourceState := report.MergeSource(next.Sources[source], source, v.External.AttemptedAt, v.External.Contact, v.Code, v.DurationMS)
		next.Sources[source] = sourceState
		if v.External.Report == nil {
			continue
		}
		binding, ok := s.options.Bindings[v.Check]
		if !ok {
			return nil, 0, errors.New("external report has no declared binding")
		}
		state, _ := report.MergeBinding(v.Check, v.Contract, binding, next.Bindings[v.Check], *v.External.Report, v.ReceivedAt)
		next.Bindings[v.Check] = state
	}
	if len(next.Sources) > model.MaxSources || len(next.Bindings) > model.MaxExternalChecks {
		return nil, 0, errors.New("external state entity budget exceeded")
	}
	// Bound the persisted map itself, not only a single report.
	raw, err := json.Marshal(next)
	if err != nil {
		return nil, 0, fmt.Errorf("external state encoding failed: %w", err)
	}
	if int64(len(raw)) > maxExternalStateBytes {
		return nil, 0, errors.New("external state byte budget exceeded")
	}
	if len(next.Sources) == 0 && len(next.Bindings) == 0 {
		return nil, 0, nil
	}
	return next, int64(len(raw)), nil
}

// installExternalLocked atomically replaces the external state after the
// journal transaction that produced it is durable.
func (s *Store) installExternalLocked(next *model.ExternalState, size int64) {
	if next != nil {
		next = model.CloneExternalState(next)
	}
	s.external = next
	if size < 0 {
		size = 0
	}
	s.stateBytes += size - s.externalBytes
	s.externalBytes = size
}
