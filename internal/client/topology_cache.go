package client

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type cachedTopology struct {
	revision string
	topology model.Config
}

// At most one immutable topology per configured collector, shared across views.
// Replacing a revision never invalidates an in-flight read's local cache value.
func (c *Client) knownTopology(endpoint string) cachedTopology {
	c.topologyMu.Lock()
	defer c.topologyMu.Unlock()
	return c.topologies[endpoint]
}

func (c *Client) rememberTopology(endpoint string, snap model.Snapshot) {
	if c.knownTopology(endpoint).revision == snap.Revision {
		return
	}
	// A peer's revision must actually identify its public topology before it
	// can be offered back as a cache validator. Legacy full snapshots still read.
	if config.Revision(snap.Topology) != snap.Revision {
		return
	}
	owned := config.Clone(snap.Topology)
	c.topologyMu.Lock()
	defer c.topologyMu.Unlock()
	if c.topologies == nil {
		c.topologies = map[string]cachedTopology{}
	}
	if c.topologies[endpoint].revision != snap.Revision {
		c.topologies[endpoint] = cachedTopology{snap.Revision, owned}
	}
}
