package model

type AdapterOptions struct {
	TokenEnv          string              `json:"token_env,omitempty"`
	RootProxy         string              `json:"root_proxy,omitempty"`
	ExpectedMode      string              `json:"expected_mode,omitempty"`
	ExpectedProviders map[string][]string `json:"expected_providers,omitempty"`
	Peer              string              `json:"peer,omitempty"`
	Interface         string              `json:"interface,omitempty"`
}
