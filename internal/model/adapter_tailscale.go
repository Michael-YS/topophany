package model

// TailscaleOptions is the software-specific view of the legacy flat adapter configuration.
type TailscaleOptions struct {
	Diagnostic string
	Peer       string
}

func (a AdapterOptions) Tailscale() TailscaleOptions {
	return TailscaleOptions{Peer: a.Peer, Diagnostic: a.TailscaleDiagnostic}
}
