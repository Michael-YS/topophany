package mihomo

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func collectProviders(ctx context.Context, deps Dependencies, client *http.Client, x model.Check, target, token string, name func(string) string, o *model.Observation) error {
	options := x.Adapter.Mihomo()
	if len(options.ProviderMatchModes) > 0 && Validate(options) != nil {
		return adapters.Local("setup", "invalid_provider_options")
	}
	if len(options.ExpectedProviders) == 0 && options.ProviderReference == nil {
		return nil
	}
	// Preserve the existing direct-run checks and diagnostic codes for static lists.
	if len(options.ExpectedProviders) > 16 {
		return adapters.Local("setup", "provider_evidence_budget_exceeded")
	}
	for k := range options.ExpectedProviders {
		if len(k) > 119 || secure.Text(k) != k {
			return adapters.Local("setup", "invalid_provider_name")
		}
	}
	expected := options.ExpectedProviders
	var before referenceSnapshot
	if options.ProviderReference != nil {
		if Validate(options) != nil || ValidateReferenceTransport(options.ProviderReference, deps.Proxied) != nil {
			return adapters.Local("setup", "invalid_provider_reference")
		}
		var err error
		before, err = readReference(ctx, deps, x)
		if err != nil {
			return err
		}
		expected = make(map[string][]string, len(options.ExpectedProviders)+len(before.Providers))
		for k, v := range options.ExpectedProviders {
			expected[k] = v
		}
		for k, v := range before.Providers {
			expected[k] = v
		}
	}
	var out struct {
		Providers map[string]struct {
			Proxies   []coreProxy `json:"proxies"`
			UpdatedAt time.Time   `json:"updatedAt"`
		} `json:"providers"`
	}
	if err := readCore(ctx, client, target, "/providers/proxies", token, &out); err != nil {
		return err
	}
	stable := true
	settling := false
	if options.ProviderReference != nil {
		after, err := readReference(ctx, deps, x)
		if err != nil {
			return err
		}
		stable = before.Revision == after.Revision
		o.Facts["provider_reference_published_at"] = before.PublishedAt.UTC().Format(time.RFC3339Nano)
		o.Facts["provider_reference_consistency"] = "stable"
		if !stable {
			o.Facts["provider_reference_consistency"] = "changed"
		}
		// A controller loads a new provider over a minute or two. Inside
		// that window a difference is not yet a violation: it is what every
		// publication looks like before the runtime catches up.
		if wait := settle(options.ProviderReference); wait > 0 && !before.PublishedAt.IsZero() && time.Since(before.PublishedAt) < wait {
			settling = true
		}
	}
	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	summaries := []string{}
	newest := time.Time{}
	for _, k := range keys {
		p, ok := out.Providers[k]
		if p.UpdatedAt.After(newest) {
			newest = p.UpdatedAt
		}
		actual, shown := []string{}, []string{}
		for _, v := range p.Proxies {
			actual = append(actual, v.Name)
			shown = append(shown, name(v.Name))
		}
		comparison := compareMembers(actual, expected[k], options.ProviderMatchModes[k])
		same := ok && comparison.matches

		o.Facts["provider."+adapters.SafeFact(k)] = adapters.SafeFact(strings.Join(shown, ","))
		_, dynamic := before.Providers[k]
		status := "match"
		if !same {
			status = "mismatch"
		}
		if dynamic && !stable {
			status = "unverified"
		}
		if dynamic && stable && settling && !same {
			status = "settling"
		}
		summaries = append(summaries, fmt.Sprintf("%s=%s(expected:%d loaded:%d missing:%d extra:%d order_changed:%t present:%t)", k, status, len(expected[k]), len(actual), comparison.missing, comparison.extra, comparison.orderChanged, ok))
		if !same && (!dynamic || stable) {
			if dynamic && settling {
				if o.Outcome != "policy" {
					o.Outcome, o.Code = "unknown", "provider_reference_settling"
					o.Detail = "provider membership differs while the controller may still be loading the publication; not yet a violation"
					o.Facts["provider_reference_settling"] = "true"
				}
				continue
			}
			o.Outcome, o.Code = "policy", "provider_membership_mismatch"
			o.Detail = "desired provider membership/order differs from the loaded runtime"
		}
	}
	if !newest.IsZero() {
		// The controller's own account of when it last loaded a provider,
		// beside the publisher's time and this sample's time.
		o.Facts["provider_updated_at"] = newest.UTC().Format(time.RFC3339Nano)
	}
	o.Facts["provider_comparison"] = adapters.SafeFact(strings.Join(summaries, "; "))
	if !stable && o.Outcome != "policy" {
		o.Outcome, o.Code = "unknown", "provider_reference_changed"
		o.Detail = "provider publication changed during sampling; loaded membership cannot be compared consistently"
	}
	return nil
}
