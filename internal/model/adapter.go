package model

type AdapterOptions struct {
	TokenEnv                 string                   `json:"token_env,omitempty"`
	RootProxy                string                   `json:"root_proxy,omitempty"`
	ExpectedMode             string                   `json:"expected_mode,omitempty"`
	ExpectedProviders        map[string][]string      `json:"expected_providers,omitempty"`
	Peer                     string                   `json:"peer,omitempty"`
	Interface                string                   `json:"interface,omitempty"`
	ProviderReference        *MihomoProviderReference `json:"provider_reference,omitempty"`
	ExpectedAllowedIPs       []string                 `json:"expected_allowed_ips,omitempty"`
	ExpectedKeepaliveSeconds *int                     `json:"expected_keepalive_seconds,omitempty"`
	ExpectedTUN              *bool                    `json:"expected_tun,omitempty"`
	ProviderMatchModes       map[string]string        `json:"provider_match_modes,omitempty"`
	TailscaleDiagnostic      string                   `json:"tailscale_diagnostic,omitempty"`
	Cloudflared              *CloudflaredOptions      `json:"cloudflared,omitempty"`
	Cloudflare               *CloudflareOptions       `json:"cloudflare,omitempty"`
}
