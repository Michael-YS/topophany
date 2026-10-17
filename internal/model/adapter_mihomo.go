package model

// MihomoOptions is the software-specific view of the legacy flat adapter configuration.
type MihomoOptions struct {
	TokenEnv           string
	RootProxy          string
	ExpectedTUN        *bool
	ProviderMatchModes map[string]string
	ExpectedMode       string
	ProviderReference  *MihomoProviderReference
	ExpectedProviders  map[string][]string
}

func (a AdapterOptions) Mihomo() MihomoOptions {
	return MihomoOptions{ExpectedTUN: a.ExpectedTUN, ProviderMatchModes: a.ProviderMatchModes, ProviderReference: a.ProviderReference, TokenEnv: a.TokenEnv, RootProxy: a.RootProxy, ExpectedMode: a.ExpectedMode, ExpectedProviders: a.ExpectedProviders}
}

// MihomoProviderReference selects dynamic membership from an independent,
// names-only publisher. MaxAgeSeconds bounds accepted publication age.
type MihomoProviderReference struct {
	URL           string   `json:"url"`
	Providers     []string `json:"providers"`
	MaxAgeSeconds int      `json:"max_age_seconds"`
	TokenEnv      string   `json:"token_env,omitempty"`
	CAFile        string   `json:"ca_file,omitempty"`
}
