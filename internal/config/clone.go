package config

import (
	"maps"
	"slices"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Clone detaches every mutable field without a JSON encode/decode cycle.
// Configurations and published snapshots must never share writable storage.
func Clone(c model.Config) model.Config {
	c.Nodes = slices.Clone(c.Nodes)
	for i := range c.Nodes {
		if l := c.Nodes[i].Location; l != nil {
			v := *l
			c.Nodes[i].Location = &v
		}
	}
	c.Endpoints = slices.Clone(c.Endpoints)
	for i := range c.Endpoints {
		c.Endpoints[i].Via = slices.Clone(c.Endpoints[i].Via)
	}
	c.Services = slices.Clone(c.Services)
	for i := range c.Services {
		s := &c.Services[i]
		s.Endpoints, s.DependsOn = slices.Clone(s.Endpoints), slices.Clone(s.DependsOn)
	}
	c.Observers, c.Links = slices.Clone(c.Observers), slices.Clone(c.Links)
	c.Paths = slices.Clone(c.Paths)
	for i := range c.Paths {
		p := &c.Paths[i]
		p.Nodes, p.Links = slices.Clone(p.Nodes), slices.Clone(p.Links)
		p.Candidates, p.DependsOn = slices.Clone(p.Candidates), slices.Clone(p.DependsOn)
		p.Proxies = slices.Clone(p.Proxies)
	}
	c.Checks = slices.Clone(c.Checks)
	for i := range c.Checks {
		x := &c.Checks[i]
		if x.Host != nil {
			h := *x.Host
			x.Host = &h
		}
		x.HTTP.Status = slices.Clone(x.HTTP.Status)
		x.HTTP.ExpectedExit = slices.Clone(x.HTTP.ExpectedExit)
		x.HTTP.Headers, x.HTTP.RequestHeaders = maps.Clone(x.HTTP.Headers), maps.Clone(x.HTTP.RequestHeaders)
		x.DNS.ExpectedIPs = slices.Clone(x.DNS.ExpectedIPs)
		x.Adapter.ExpectedProviders = maps.Clone(x.Adapter.ExpectedProviders)
		for k, names := range x.Adapter.ExpectedProviders {
			x.Adapter.ExpectedProviders[k] = slices.Clone(names)
		}
	}
	return c
}
