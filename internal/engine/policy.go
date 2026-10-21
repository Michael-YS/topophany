package engine

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// providerSettling applies the collector's current window to evidence taken
// at the sample's own time. A worker's configured window may differ after a
// reload, and queued samples retain the configuration they were taken under.
func providerSettling(x model.Check, last model.Observation) bool {
	ref := x.Adapter.ProviderReference
	if ref == nil || ref.SettleSecondsOrDefault() <= 0 {
		return false
	}
	legacy := last.Facts["provider_reference_settling"] == "true"
	switch last.Facts["provider_reference_load_pending"] {
	case "false":
		// At least one violation is independent of a pending provider load.
		return false
	case "true":
		// Every known violation could still be the publication loading.
	default:
		// Older workers recorded only their window-dependent decision. An
		// absent marker cannot exempt a fresh publication from a newly
		// extended window. Keep explicit mode/TUN violations independent;
		// otherwise conservatively wait when old evidence is insufficient.
		if !legacy && (last.Code != "provider_membership_mismatch" || last.Facts["provider_reference_consistency"] != "stable" || knownRuntimePolicy(x.Adapter, last.Facts)) {
			return false
		}
	}
	published, err := time.Parse(time.RFC3339Nano, last.Facts["provider_reference_published_at"])
	if err != nil || published.IsZero() || last.SampledAt.IsZero() {
		return legacy
	}
	return last.SampledAt.Sub(published) < time.Duration(ref.SettleSecondsOrDefault())*time.Second
}

func knownRuntimePolicy(a model.AdapterOptions, facts map[string]string) bool {
	if mode := facts["mode"]; a.ExpectedMode != "" && mode != "" && mode != "other" && mode != a.ExpectedMode {
		return true
	}
	if want := a.ExpectedTUN; want != nil {
		actual := facts["tun_enabled"]
		return actual == "true" && !*want || actual == "false" && *want
	}
	return false
}

// ConfirmationSamples is how many consecutive policy samples count as
// confirmed for the evidence in last. While that evidence is still inside
// a provider reference's settle window, the count covers the window.
func ConfirmationSamples(x model.Check, last *model.Observation) int {
	if last == nil {
		return policyThreshold(x, false)
	}
	return policyThreshold(x, providerSettling(x, *last))
}

// policyThreshold is how many consecutive policy samples a check needs
// before it opens an incident. Carry retains the run across restarts and
// publications, so repeated publications cannot postpone confirmation forever.
func policyThreshold(x model.Check, settling bool) int {
	threshold := max(x.PolicyThreshold, 1)
	ref := x.Adapter.ProviderReference
	if ref == nil || !settling {
		return threshold
	}
	settle := ref.SettleSecondsOrDefault()
	if settle <= 0 || x.IntervalSeconds <= 0 {
		return threshold
	}
	samples := (settle + x.IntervalSeconds - 1) / x.IntervalSeconds
	return max(threshold, samples)
}
