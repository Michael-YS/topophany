package store

import (
	"maps"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// TopologyContext is the correction-critical part of a store's Options:
// the revision and contracts used to count samples, and the external
// bindings, sources and thresholds needed while recovering a journal. It is
// built by WithTopology for production and tests alike; budgets stay with the
// caller.
type TopologyContext struct {
	Revision      string
	Contracts     map[string]string
	Stale         map[string]time.Duration
	Bindings      map[string]model.ExternalBinding
	Sources       map[string]model.Source
	ExternalRules map[string]model.ExternalRule
}

// ContextFromConfig derives the store context from a validated topology.
func ContextFromConfig(c model.Config) TopologyContext {
	sources := make(map[string]model.Source, len(c.Sources))
	for _, source := range c.Sources {
		sources[source.ID] = source
	}
	bindings := make(map[string]model.ExternalBinding)
	rules := make(map[string]model.ExternalRule)
	for _, x := range c.Checks {
		if x.Kind == "external" && x.External != nil {
			bindings[x.ID] = *x.External
			rules[x.ID] = model.ExternalRule{FailureThreshold: x.FailureThreshold, RecoveryThreshold: x.RecoveryThreshold, PolicyThreshold: x.PolicyThreshold}
		}
	}
	return TopologyContext{
		Revision:      config.Revision(c),
		Contracts:     config.Contracts(c),
		Stale:         config.Stale(c),
		Bindings:      bindings,
		Sources:       sources,
		ExternalRules: rules,
	}
}

// WithTopology returns a copy of o carrying the context a store needs before
// Store.Open replays a journal. A caller cannot forget a binding that a
// recovered external record needs while still supplying the same context to a
// later SetExternalContext reload.
func (o Options) WithTopology(c model.Config) Options {
	ctx := ContextFromConfig(c)
	o.Revision = ctx.Revision
	o.Contracts = maps.Clone(ctx.Contracts)
	o.Stale = maps.Clone(ctx.Stale)
	o.Bindings = ctx.Bindings
	o.Sources = ctx.Sources
	o.ExternalRules = ctx.ExternalRules
	return o
}
