package report

import (
	"encoding/json"
	"maps"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// StateUpdate owns new containers and changed entries. Unaffected entries are
// immutable and shared with the input, so admission can abort without rollback.
type StateUpdate struct {
	State    *model.ExternalState
	Sources  map[string]model.SourceState
	Bindings map[string]model.BindingState
}

func BeginState(state *model.ExternalState) *StateUpdate {
	next := model.NewExternalState()
	if state != nil {
		for k, v := range state.Sources {
			next.Sources[k] = v
		}
		for k, v := range state.Bindings {
			next.Bindings[k] = v
		}
	}
	return &StateUpdate{State: next, Sources: map[string]model.SourceState{}, Bindings: map[string]model.BindingState{}}
}

// Apply accepts an already normalized, admitted observation. Source contact
// and report identity advance independently; errors never refresh report data.
func (u *StateUpdate) Apply(v model.Observation, binding model.ExternalBinding, rule model.ExternalRule) {
	if v.External == nil {
		return
	}
	id := v.External.Source
	state := MergeSource(u.State.Sources[id], id, v.External.AttemptedAt, v.External.Contact, v.Code, v.DurationMS)
	u.State.Sources[id], u.Sources[id] = state, state
	if v.External.Report == nil {
		return
	}
	current := u.State.Bindings[v.Check]
	if _, changed := u.Bindings[v.Check]; !changed {
		current = model.CloneBindingState(current)
	}
	current = ApplyRule(current, rule)
	current, _ = MergeBinding(v.Check, v.Contract, binding, current, *v.External.Report, v.ReceivedAt)
	u.State.Bindings[v.Check], u.Bindings[v.Check] = current, current
}

// StateSizes uses the same conservative encoded accounting for durable and
// process-local report state. It serializes only changed entries on updates.
type StateSizes struct {
	Total   int64
	Entries map[string]int64
	Sources map[string]int64
}

const StateOverhead = 64

func StateEntryBytes(key string, value any) (int64, error) {
	raw, err := json.Marshal(value)
	return int64(len(key) + len(raw) + 4), err
}

func MeasureState(state *model.ExternalState) (StateSizes, error) {
	sizes := StateSizes{Total: StateOverhead, Entries: map[string]int64{}, Sources: map[string]int64{}}
	if state == nil {
		return sizes, nil
	}
	return sizes.Updated(&StateUpdate{Sources: state.Sources, Bindings: state.Bindings})
}

func (s StateSizes) Updated(u *StateUpdate) (StateSizes, error) {
	next := StateSizes{Total: s.Total, Entries: maps.Clone(s.Entries), Sources: maps.Clone(s.Sources)}
	if next.Entries == nil {
		next.Entries = map[string]int64{}
	}
	if next.Sources == nil {
		next.Sources = map[string]int64{}
	}
	add := func(key, id string, value any) error {
		size, err := StateEntryBytes(key[2:], value)
		if err != nil {
			return err
		}
		delta := size - next.Entries[key]
		next.Total += delta
		next.Sources[id] += delta
		next.Entries[key] = size
		return nil
	}
	for id, state := range u.Sources {
		if err := add("s:"+id, id, state); err != nil {
			return StateSizes{}, err
		}
	}
	for id, state := range u.Bindings {
		if err := add("b:"+id, state.Source, state); err != nil {
			return StateSizes{}, err
		}
	}
	return next, nil
}

// ApplyRule rejudges runs only when their effective thresholds change.
// Confirmed evidence can outlive a run reset across a sampling gap, so replay,
// duplicates and metadata reloads must preserve the persisted confirmation.
func ApplyRule(state model.BindingState, rule model.ExternalRule) model.BindingState {
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
	oldFailure, oldRecovery := state.FailureThreshold, state.RecoveryThreshold
	if oldFailure < 1 {
		oldFailure = 3
	}
	if oldRecovery < 1 {
		oldRecovery = 2
	}
	if oldFailure != failure || oldRecovery != recovery {
		if state.ConfirmedDown {
			state.ConfirmedDown = state.UpRun < int64(recovery)
		} else {
			state.ConfirmedDown = state.DownRun >= int64(failure)
		}
	}
	if max(state.PolicyThreshold, 1) != max(policy, 1) {
		state.PolicyConfirmed = state.PolicyRun >= int64(max(policy, 1))
	}
	state.FailureThreshold, state.RecoveryThreshold, state.PolicyThreshold = failure, recovery, policy
	return state
}
