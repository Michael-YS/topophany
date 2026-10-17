package model

// WireGuardOptions is the software-specific view of the legacy flat adapter configuration.
type WireGuardOptions struct {
	Peer                     string
	Interface                string
	ExpectedAllowedIPs       []string
	ExpectedKeepaliveSeconds *int
}

func (a AdapterOptions) WireGuard() WireGuardOptions {
	return WireGuardOptions{Peer: a.Peer, Interface: a.Interface, ExpectedAllowedIPs: a.ExpectedAllowedIPs, ExpectedKeepaliveSeconds: a.ExpectedKeepaliveSeconds}
}
