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

// DefaultProviderSettleSeconds is the default wait after a publication
// before its membership difference is treated as a policy drift.
const DefaultProviderSettleSeconds = 120

// SettleSecondsOrDefault is the configured wait after PublishedAt: the
// default when unset, none when zero.
func (r *MihomoProviderReference) SettleSecondsOrDefault() int {
	if r == nil || r.SettleSeconds == nil {
		return DefaultProviderSettleSeconds
	}
	return *r.SettleSeconds
}

// MihomoProviderReference selects dynamic membership from an independent,
// names-only publisher. MaxAgeSeconds bounds accepted publication age.
type MihomoProviderReference struct {
	URL           string   `json:"url"`
	Providers     []string `json:"providers"`
	MaxAgeSeconds int      `json:"max_age_seconds"`
	// SettleSeconds is how long after PublishedAt a membership difference is
	// treated as not yet confirmed, because the controller may still be
	// loading the publication. The engine turns it into consecutive policy
	// samples at the check's interval, so repeated publications cannot defer
	// confirmation forever. Nil uses the default, 0 disables the wait.
	SettleSeconds *int   `json:"settle_seconds,omitempty"`
	TokenEnv      string `json:"token_env,omitempty"`
	CAFile        string `json:"ca_file,omitempty"`
}
