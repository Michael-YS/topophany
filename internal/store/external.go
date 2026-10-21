package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
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
	ctx := ContextFromConfig(c)
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.Bindings, s.options.Sources, s.options.ExternalRules = ctx.Bindings, ctx.Sources, ctx.ExternalRules
	s.pruneExternalLocked(ctx.Contracts)
	s.applyExternalRulesLocked()
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

const (
	// externalStateOverhead covers the object braces, schema and map commas.
	externalStateOverhead = 64
	// When several sources share a store, no source may consume the whole
	// external state budget: half stays available for the others. A
	// single-source store has nobody to isolate from and may use the budget.
	// A configuration whose retention cannot fit is refused explicitly
	// rather than silently evicting accepted evidence.
	maxExternalSourceBytes = maxExternalStateBytes / 2
)

func (s *Store) externalSourceLimit() int64 {
	if len(s.options.Sources) <= 1 {
		return maxExternalStateBytes
	}
	return maxExternalSourceBytes
}

type externalCommit struct {
	state         *model.ExternalState
	size          int64
	sizes         map[string]int64
	sourceBytes   map[string]int64
	observerBytes map[string]int64
}

func externalEntrySize(key string, value any) (int64, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	return int64(len(key) + len(raw) + 4), nil
}

// externalAccountingLocked derives per-entry and per-source encoded sizes
// without serializing state that did not change. Callers hold s.mu.
func (s *Store) externalAccountingLocked() (int64, map[string]int64, map[string]int64, map[string]int64, error) {
	sizes := map[string]int64{}
	bySource := map[string]int64{}
	byObserver := map[string]int64{}
	if s.external == nil {
		return 0, sizes, bySource, byObserver, nil
	}
	total := int64(externalStateOverhead)
	for id, state := range s.external.Sources {
		size, err := externalEntrySize(id, state)
		if err != nil {
			return 0, nil, nil, nil, err
		}
		sizes["s:"+id] = size
		bySource[id] += size
		byObserver[s.sourceObserverLocked(id)] += size
		total += size
	}
	for check, state := range s.external.Bindings {
		size, err := externalEntrySize(check, state)
		if err != nil {
			return 0, nil, nil, nil, err
		}
		sizes["b:"+check] = size
		bySource[state.Source] += size
		byObserver[s.sourceObserverLocked(state.Source)] += size
		total += size
	}
	return total, sizes, bySource, byObserver, nil
}

func (s *Store) sourceObserverLocked(source string) string {
	if state, ok := s.options.Sources[source]; ok {
		return state.Observer
	}
	return ""
}

// setExternalObserverBytesLocked applies only the per-observer share of an
// accounting change. The global stateBytes delta is handled separately, so
// this must not call adjustBytes.
func (s *Store) setExternalObserverBytesLocked(next map[string]int64) {
	if s.scopeBytes == nil {
		s.scopeBytes = map[string]int64{}
	}
	if next == nil {
		next = map[string]int64{}
	}
	for observer, size := range next {
		if delta := size - s.externalObserverBytes[observer]; delta != 0 {
			s.scopeBytes[observer] += delta
			if s.scopeBytes[observer] == 0 {
				delete(s.scopeBytes, observer)
			}
		}
	}
	for observer, size := range s.externalObserverBytes {
		if _, ok := next[observer]; !ok {
			s.scopeBytes[observer] -= size
			if s.scopeBytes[observer] == 0 {
				delete(s.scopeBytes, observer)
			}
		}
	}
	s.externalObserverBytes = next
}

func (s *Store) applyExternalAccountingLocked(size int64, sizes, bySource, byObserver map[string]int64) {
	if size < 0 {
		size = 0
	}
	s.stateBytes += size - s.externalBytes
	s.externalBytes = size
	s.externalSizes = sizes
	s.externalSourceBytes = bySource
	s.setExternalObserverBytesLocked(byObserver)
}

// rebuildExternalAccountingLocked is used on recovery and context changes,
// not on the write hot path.
func (s *Store) rebuildExternalAccountingLocked() {
	size, sizes, bySource, byObserver, err := s.externalAccountingLocked()
	if err != nil {
		s.poisoned = fmt.Errorf("external state encoding failed: %w", err)
		return
	}
	s.applyExternalAccountingLocked(size, sizes, bySource, byObserver)
}

func (s *Store) ensureExternalAccountingLocked() {
	if s.externalSizes == nil || s.externalSourceBytes == nil || s.externalObserverBytes == nil {
		s.rebuildExternalAccountingLocked()
	}
}

// resizeExternalLocked recomputes serialized state bytes and collapses an
// empty state to nil. Callers hold s.mu.
func (s *Store) resizeExternalLocked() {
	if s.external == nil || len(s.external.Sources) == 0 && len(s.external.Bindings) == 0 {
		s.external = nil
		s.applyExternalAccountingLocked(0, map[string]int64{}, map[string]int64{}, map[string]int64{})
		return
	}
	s.rebuildExternalAccountingLocked()
}

// admitExternalLocked normalizes and live-admits one external observation
// against the installed context. It is also the direct-local-collector
// admission path, which does not pass through the API. Callers hold
// commitMu and s.mu.
func (s *Store) admitExternalLocked(v *model.Observation, now, future time.Time) error {
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
	x := model.Check{ID: v.Check, Observer: source.Observer, Kind: "external", External: &binding, TimeoutSeconds: source.TimeoutSeconds}
	normalized, err := admission.External(*v, x, source, admission.Window{Now: now, Future: future, MaxAge: s.options.Retention, Skew: 30 * time.Second})
	if err != nil {
		return fmt.Errorf("external observation: %w", err)
	}
	v.External.Report = normalized
	return nil
}

// mergeExternalLocked derives the next external state from the observations
// this transaction will commit. It copies the two top-level maps and only
// clones the sources and bindings this transaction updates; unaffected
// bindings keep their immutable pointers and Seen slices. It does not mutate
// the live state or its accounting: Put installs the result only after the
// journal is durable.
func (s *Store) mergeExternalLocked(observations []model.Observation) (*externalCommit, error) {
	s.ensureExternalAccountingLocked()
	sources := map[string]model.SourceState{}
	bindings := map[string]model.BindingState{}
	if s.external != nil {
		sources = maps.Clone(s.external.Sources)
		bindings = maps.Clone(s.external.Bindings)
	}
	if sources == nil {
		sources = map[string]model.SourceState{}
	}
	if bindings == nil {
		bindings = map[string]model.BindingState{}
	}
	affectedSources := map[string]bool{}
	affectedBindings := map[string]bool{}
	for _, v := range observations {
		if v.External == nil || !s.current(v) {
			continue
		}
		source := v.External.Source
		if _, ok := s.options.Sources[source]; !ok {
			// A removed source cannot be made current again by replay.
			continue
		}
		affectedSources[source] = true
		sources[source] = report.MergeSource(sources[source], source, v.External.AttemptedAt, v.External.Contact, v.Code, v.DurationMS)
		if v.External.Report == nil {
			continue
		}
		binding, ok := s.options.Bindings[v.Check]
		if !ok {
			continue
		}
		if _, ok := s.options.Sources[binding.Source]; !ok {
			continue
		}
		if !affectedBindings[v.Check] {
			bindings[v.Check] = model.CloneBindingState(bindings[v.Check])
			affectedBindings[v.Check] = true
		}
		state := applyExternalRule(bindings[v.Check], s.options.ExternalRules[v.Check])
		state, _ = report.MergeBinding(v.Check, v.Contract, binding, state, *v.External.Report, v.ReceivedAt)
		bindings[v.Check] = state
	}
	if len(affectedSources) == 0 && len(affectedBindings) == 0 {
		return nil, nil
	}
	if len(sources) > model.MaxSources || len(bindings) > model.MaxExternalChecks {
		return nil, errors.New("external state entity budget exceeded")
	}
	touchedSources := map[string]bool{}
	for source := range affectedSources {
		touchedSources[source] = true
	}
	for check := range affectedBindings {
		touchedSources[bindings[check].Source] = true
	}
	sizes := make(map[string]int64, len(affectedSources)+len(affectedBindings))
	bySource := maps.Clone(s.externalSourceBytes)
	if bySource == nil {
		bySource = map[string]int64{}
	}
	byObserver := maps.Clone(s.externalObserverBytes)
	if byObserver == nil {
		byObserver = map[string]int64{}
	}
	total := s.externalBytes
	add := func(key, source string, value any) error {
		size, err := externalEntrySize(key[2:], value)
		if err != nil {
			return fmt.Errorf("external state encoding failed: %w", err)
		}
		delta := size - s.externalSizes[key]
		total += delta
		bySource[source] += delta
		byObserver[s.sourceObserverLocked(source)] += delta
		sizes[key] = size
		return nil
	}
	for source := range affectedSources {
		if err := add("s:"+source, source, sources[source]); err != nil {
			return nil, err
		}
	}
	for check := range affectedBindings {
		state := bindings[check]
		if err := add("b:"+check, state.Source, state); err != nil {
			return nil, err
		}
	}
	if total > maxExternalStateBytes {
		return nil, errors.New("external state byte budget exceeded")
	}
	limit := s.externalSourceLimit()
	for source := range touchedSources {
		if bySource[source] > limit {
			return nil, errors.New("external source state budget exceeded")
		}
	}
	// External state is charged to the observer that owns the source. When
	// observers are configured, one observer's report state cannot silently
	// consume another's share of the global state budget.
	if len(s.options.Observers) > 0 {
		for observer, size := range byObserver {
			if size > s.options.byteShare(observer) {
				return nil, fmt.Errorf("%w: external report state", ErrScopeCapacity)
			}
		}
	}
	next := &model.ExternalState{Schema: model.ExternalStateSchema, Sources: sources, Bindings: bindings}
	return &externalCommit{state: next, size: total, sizes: sizes, sourceBytes: bySource, observerBytes: byObserver}, nil
}

// installExternalLocked atomically replaces the external state after the
// journal transaction that produced it is durable. The commit owns its maps;
// unaffected binding values are immutable and may be shared with the previous
// map, which is never mutated in place.
func (s *Store) installExternalLocked(commit *externalCommit) {
	if commit == nil {
		return
	}
	if commit.state == nil || len(commit.state.Sources) == 0 && len(commit.state.Bindings) == 0 {
		s.external = nil
		s.applyExternalAccountingLocked(0, map[string]int64{}, map[string]int64{}, map[string]int64{})
		return
	}
	s.external = commit.state
	for key, size := range commit.sizes {
		s.externalSizes[key] = size
	}
	s.externalSourceBytes = commit.sourceBytes
	s.setExternalObserverBytesLocked(commit.observerBytes)
	s.stateBytes += commit.size - s.externalBytes
	s.externalBytes = commit.size
}

// applyExternalRulesLocked re-applies confirmation thresholds to persisted
// runs when the topology reloads. Thresholds are not measurement identity, so
// this changes judgment without discarding the runs or report.
func (s *Store) applyExternalRulesLocked() {
	if s.external == nil {
		return
	}
	for check, state := range s.external.Bindings {
		s.external.Bindings[check] = applyExternalRule(state, s.options.ExternalRules[check])
	}
	s.resizeExternalLocked()
}

// applyExternalRule sets the thresholds and resolves what the already
// persisted runs mean under them. It never increments a run.
func applyExternalRule(state model.BindingState, rule model.ExternalRule) model.BindingState {
	failure, recovery, policy := rule.FailureThreshold, rule.RecoveryThreshold, rule.PolicyThreshold
	if failure < 1 {
		failure = 3
	}
	if recovery < 1 {
		recovery = 2
	}
	if policy < 0 {
		policy = 0
	}
	state.FailureThreshold, state.RecoveryThreshold, state.PolicyThreshold = failure, recovery, policy
	if state.ConfirmedDown {
		state.ConfirmedDown = state.UpRun < int64(recovery)
	} else {
		state.ConfirmedDown = state.DownRun >= int64(failure)
	}
	threshold := max(policy, 1)
	state.PolicyConfirmed = state.PolicyRun >= int64(threshold)
	return state
}
