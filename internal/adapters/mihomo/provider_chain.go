package mihomo

import "github.com/WhitenWhiten/topophany/internal/adapters"

func mergeProviderProxies(all map[string]coreProxy, providers coreProviders) error {
	added, ambiguous := map[string]coreProxy{}, map[string]bool{}
	for _, provider := range providers.Providers {
		for _, p := range provider.Proxies {
			if _, static := all[p.Name]; static || p.Name == "" {
				continue
			}
			if _, seen := added[p.Name]; seen {
				ambiguous[p.Name] = true
			}
			added[p.Name] = p
			if len(all)+len(added) > maxCoreProxies {
				return adapters.Local("adapter_api", "proxy_inventory_capacity_exceeded")
			}
		}
	}
	for name, p := range added {
		if !ambiguous[name] {
			all[name] = p
		}
	}
	return nil
}
