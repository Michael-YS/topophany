package cloudflare

import (
	"encoding/json"
	"strconv"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// poolResponse reads the pool's health report. pop_health is kept as raw JSON
// because Cloudflare's reference renders it two ways: a single object with
// healthy and origins, and "a list of regions and associated health status"
// that the generated model collapses into that same object. Both documented
// renderings are accepted; each region object carries an optional healthy flag
// and an origins array whose objects are keyed by origin address. Origin
// addresses are compared with the declared list and never stored as strings.
type poolResponse struct {
	Success *bool `json:"success"`
	Result  *struct {
		PopHealth json.RawMessage `json:"pop_health"`
	} `json:"result"`
}

type regionHealth struct {
	Healthy *bool                     `json:"healthy"`
	Origins []map[string]originHealth `json:"origins"`
}

type originHealth struct {
	Healthy *bool `json:"healthy"`
}

// collectPool turns the pool health read into facts and the declared-origin
// policy. Only declared origin addresses appear as fact keys. Undeclared
// origins contribute to the two counts but never to a key, a value or a
// detail.
func collectPool(body []byte, options model.CloudflareOptions, o *model.Observation) error {
	var doc poolResponse
	if err := decodeJSON(body, &doc); err != nil {
		return err
	}
	if err := checkEnvelope(doc.Success); err != nil {
		return err
	}
	if doc.Result == nil {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	regions, err := parseRegions(doc.Result.PopHealth)
	if err != nil {
		return err
	}
	o.Outcome = "ok"
	o.Facts["regions_reporting"] = strconv.Itoa(len(regions))

	// total is every distinct origin the report names; unhealthy is every
	// origin at least one region does not call healthy. An origin reported in
	// several regions must be healthy in all of them.
	total, unhealthy := map[string]bool{}, map[string]bool{}
	poolHealthy, poolHealthyKnown := true, false
	for _, region := range regions {
		if region.Healthy != nil {
			if !*region.Healthy {
				poolHealthy = false
			}
			poolHealthyKnown = true
		}
		for _, origins := range region.Origins {
			for address, health := range origins {
				key, ok := originKey(address)
				if !ok {
					// An address this adapter cannot shape is not evidence
					// about a declared origin, but it still counts as a
					// reported origin.
					key = address
				}
				total[key] = true
				if health.Healthy == nil || !*health.Healthy {
					unhealthy[key] = true
				}
			}
		}
	}
	healthy := 0
	for key := range total {
		if !unhealthy[key] {
			healthy++
		}
	}
	o.Facts["origins_total"] = strconv.Itoa(len(total))
	o.Facts["origins_healthy"] = strconv.Itoa(healthy)
	if poolHealthyKnown {
		o.Facts["pool_healthy"] = strconv.FormatBool(poolHealthy)
	}

	bad := false
	for _, declared := range options.ExpectedOrigins {
		key, _ := originKey(declared)
		fact := "origin." + declared
		if !total[key] {
			// No region named this origin, so no region gave a verdict. The
			// declared state is still violated, but "unhealthy" would invent a
			// reading the platform never made.
			bad = true
			continue
		}
		if unhealthy[key] {
			o.Facts[fact] = "unhealthy"
			bad = true
			continue
		}
		o.Facts[fact] = "healthy"
	}
	if bad {
		o.Outcome, o.Code = "policy", "origins_below_expected"
		o.Detail = "a declared pool origin is unhealthy or absent from Cloudflare's health report"
	}
	return nil
}

// parseRegions accepts the two documented pop_health renderings. An object
// with a healthy or origins key is one region; otherwise each key names a
// region and its value is that region's object. An absent, null or empty
// pop_health is not a health report this adapter can use.
func parseRegions(raw json.RawMessage) ([]regionHealth, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, adapters.Local("adapter_api", "unsupported_response_shape")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, adapters.Local("adapter_api", "unsupported_response_shape")
	}
	if _, ok := fields["healthy"]; ok {
		var region regionHealth
		if err := json.Unmarshal(raw, &region); err != nil {
			return nil, adapters.Local("adapter_api", "unsupported_response_shape")
		}
		return []regionHealth{region}, nil
	}
	if _, ok := fields["origins"]; ok {
		var region regionHealth
		if err := json.Unmarshal(raw, &region); err != nil {
			return nil, adapters.Local("adapter_api", "unsupported_response_shape")
		}
		return []regionHealth{region}, nil
	}
	if len(fields) == 0 {
		return nil, adapters.Local("adapter_api", "unsupported_response_shape")
	}
	regions := make([]regionHealth, 0, len(fields))
	for _, value := range fields {
		var region regionHealth
		if err := json.Unmarshal(value, &region); err != nil {
			return nil, adapters.Local("adapter_api", "unsupported_response_shape")
		}
		regions = append(regions, region)
	}
	return regions, nil
}
