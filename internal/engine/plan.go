package engine

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// Plan is the immutable part of evaluating one topology generation. A
// collector reuses it for every observer view and background ledger tick.
// NewPlan owns its configuration; Build returns independently writable data.
type Plan struct {
	config          model.Config
	public          model.Config
	revision        string
	publicRevision  string
	contracts       map[string]string
	publicContracts map[string]string
	observers       map[string]string
	checks          map[string]model.Check
	sources         map[string]model.Source
	endpoints       map[string]model.Endpoint
	paths           map[string]model.Path
	services        map[string]model.Service
	nodeChecks      map[string][]string
	endpointChecks  map[string][]string
	pathChecks      map[string][]string
	linkChecks      map[string][]string
	serviceChecks   map[string][]string
	linkPaths       map[string][]string
	incidents       map[string]string
}

func NewPlan(c model.Config) *Plan {
	c = config.Clone(c)
	public := config.Public(c)
	p := &Plan{
		config: c, public: public,
		revision: config.Revision(c), publicRevision: config.Revision(public),
		contracts: config.Contracts(c), publicContracts: config.Contracts(public),
		checks: map[string]model.Check{}, sources: map[string]model.Source{}, endpoints: map[string]model.Endpoint{},
		paths: map[string]model.Path{}, services: map[string]model.Service{},
		nodeChecks: map[string][]string{}, endpointChecks: map[string][]string{},
		pathChecks: map[string][]string{}, linkChecks: map[string][]string{}, serviceChecks: map[string][]string{},
		linkPaths: map[string][]string{}, incidents: map[string]string{},
	}
	p.observers = config.ObserverDigests(c, p.contracts)
	for _, source := range c.Sources {
		p.sources[source.ID] = source
	}
	for _, e := range c.Endpoints {
		p.endpoints[e.ID] = e
	}
	for _, path := range c.Paths {
		p.paths[path.ID] = path
		for _, id := range path.Links {
			p.linkPaths[id] = append(p.linkPaths[id], path.ID)
		}
	}
	for _, s := range c.Services {
		p.services[s.ID] = s
	}
	measured := map[string][]string{}
	for _, x := range c.Checks {
		p.checks[x.ID] = x
		// External resources/status are reported evidence, not measured
		// connectivity. They never vote in endpoint, path, link, node or
		// service measured aggregation; their own projection carries them.
		if x.Kind == "external" {
			continue
		}
		p.endpointChecks[x.Endpoint] = append(p.endpointChecks[x.Endpoint], x.ID)
		p.pathChecks[x.Path] = append(p.pathChecks[x.Path], x.ID)
		p.linkChecks[x.Link] = append(p.linkChecks[x.Link], x.ID)
		p.serviceChecks[x.Service] = append(p.serviceChecks[x.Service], x.ID)
		if ep, ok := p.endpoints[x.Endpoint]; ok {
			p.nodeChecks[ep.Node] = append(p.nodeChecks[ep.Node], x.ID)
			for _, node := range ep.Via {
				p.nodeChecks[node] = append(p.nodeChecks[node], x.ID)
			}
		}
		scope := p.scope(x)
		measured[scope] = append(measured[scope], p.publicContracts[x.ID])
	}
	for scope, contracts := range measured {
		p.incidents[scope] = digest(contracts)[:12]
	}
	return p
}

func (p *Plan) scope(x model.Check) string {
	switch {
	case x.Path != "":
		return x.Observer + "/path/" + x.Path
	case x.Endpoint != "":
		return x.Observer + "/asset/" + p.endpoints[x.Endpoint].Node
	default:
		return x.Observer + "/" + x.ID
	}
}
