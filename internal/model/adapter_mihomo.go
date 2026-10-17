package model

// MihomoOptions is the software-specific view of the legacy flat adapter configuration.
type MihomoOptions struct {
	TokenEnv          string
	RootProxy         string
	ExpectedMode      string
	ExpectedProviders map[string][]string
}

func (a AdapterOptions) Mihomo() MihomoOptions {
	return MihomoOptions{TokenEnv: a.TokenEnv, RootProxy: a.RootProxy, ExpectedMode: a.ExpectedMode, ExpectedProviders: a.ExpectedProviders}
}
