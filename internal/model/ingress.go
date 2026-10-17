package model

import "time"

// Ingress sums TLS handshake failures the collector saw before a request
// carried any observer identity. It is evidence about delivery into the
// collector, not about the targets observers watch: a failed handshake
// means the evidence it carried never arrived, so the checks behind it read
// stale rather than failed. A source stays an address: the observer names
// itself only after the handshake succeeds.
type Ingress struct {
	WindowSeconds int             `json:"window_seconds"`
	Failures      int             `json:"failures"`
	Last          time.Time       `json:"last,omitzero"`
	Sources       []IngressSource `json:"sources,omitempty"`
}

// IngressSource is one remote address's share of those failures.
type IngressSource struct {
	Address string    `json:"address"`
	Count   int       `json:"count"`
	Last    time.Time `json:"last"`
}
