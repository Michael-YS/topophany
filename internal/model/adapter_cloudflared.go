package model

import "slices"

// CloudflaredOptions is the software-specific view of adapter.cloudflared.
// It declares only the redundancy the deployment expects; the connector's
// metrics listener is read without any credential.
type CloudflaredOptions struct {
	// ExpectedConnections is the number of edge sessions the deployment
	// declares. One or more ready sessions below it is a policy violation,
	// never a failed request; zero ready sessions is a failed check before
	// policy is judged.
	ExpectedConnections *int `json:"expected_connections,omitempty"`
	// ExpectedColos lists the colos the connector's connections may report.
	// The listener labels a colo with the lowercase edge_location word,
	// while Cloudflare writes colos uppercase elsewhere, so entries may be
	// either case and are compared without it.
	ExpectedColos []string `json:"expected_colos,omitempty"`
}

// CloudflaredOptions returns the typed view of the legacy flat adapter
// configuration, detached from the adapter it was read from: the count and
// the colo list are copies, not aliases.
func (a AdapterOptions) CloudflaredOptions() CloudflaredOptions {
	if a.Cloudflared == nil {
		return CloudflaredOptions{}
	}
	v := *a.Cloudflared
	v.ExpectedColos = slices.Clone(a.Cloudflared.ExpectedColos)
	if a.Cloudflared.ExpectedConnections != nil {
		n := *a.Cloudflared.ExpectedConnections
		v.ExpectedConnections = &n
	}
	return v
}

// LocalConnectorPathEvidence marks a reported sample taken from a local
// connector process, such as cloudflared's /ready. The engine keeps such a
// sample's own up/down verdict on a link; every other reported-only sample
// stays unknown there.
const LocalConnectorPathEvidence = "local_connector_runtime_reported"
