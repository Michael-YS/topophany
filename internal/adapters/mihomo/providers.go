package mihomo

import (
	"context"
	"encoding/json"
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
			Proxies   []coreProxy     `json:"proxies"`
			UpdatedAt json.RawMessage `json:"updatedAt"`
		} `json:"providers"`
	}
	if err := readCore(ctx, client, target, "/providers/proxies", token, &out); err != nil {
		return err
	}
	stable := true
	settlingWindow := false
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
		// that window a difference is not yet confirmed; the sample still
		// says policy, and the engine needs consecutive policy samples
		// (including this window) before it opens an incident. The window
		// is measured from the sample's own time, not the wall clock at
		// comparison time, so a replay keeps its original decision.
		at := o.SampledAt
		if at.IsZero() {
			at = time.Now()
		}
		if wait := settle(options.ProviderReference); wait > 0 && !before.PublishedAt.IsZero() && at.Sub(before.PublishedAt) < wait {
			settlingWindow = true
		}
	}
	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	summaries := []string{}
	mismatched, unconfirmed := 0, 0
	for _, k := range keys {
		p, ok := out.Providers[k]
		updatedAt := providerUpdatedAt(p.UpdatedAt)
		if !updatedAt.IsZero() {
			// The controller's own account of when it last loaded this
			// provider, beside the publisher's time and the sample's time.
			o.Facts["provider."+adapters.SafeFact(k)+".updated_at"] = updatedAt.UTC().Format(time.RFC3339Nano)
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
		// The controller may still be loading a fresh publication. If it
		// says it loaded this provider after that publication, it is not
		// still loading: the difference is real.
		providerSettling := dynamic && stable && settlingWindow && !same &&
			(updatedAt.IsZero() || !updatedAt.After(before.PublishedAt))
		if providerSettling {
			status = "settling"
		}
		summaries = append(summaries, fmt.Sprintf("%s=%s(expected:%d loaded:%d missing:%d extra:%d order_changed:%t present:%t)", k, status, len(expected[k]), len(actual), comparison.missing, comparison.extra, comparison.orderChanged, ok))
		if !same && (!dynamic || stable) {
			mismatched++
			if providerSettling {
				unconfirmed++
			}
			o.Outcome, o.Code = "policy", "provider_membership_mismatch"
			o.Detail = "desired provider membership/order differs from the loaded runtime"
		}
	}
	if mismatched > 0 && unconfirmed == mismatched {
		// Every difference is inside a window the controller may still be
		// loading. It is a policy sample for confirmation purposes, not an
		// unknown: a publisher that keeps republishing cannot reset it.
		o.Facts["provider_reference_settling"] = "true"
		o.Detail = "provider membership differs while the controller may still be loading the publication; not yet confirmed"
	}
	o.Facts["provider_comparison"] = adapters.SafeFact(strings.Join(summaries, "; "))
	if !stable && o.Outcome != "policy" {
		o.Outcome, o.Code = "unknown", "provider_reference_changed"
		o.Detail = "provider publication changed during sampling; loaded membership cannot be compared consistently"
	}
	return nil
}

// providerUpdatedAt parses the controller's optional loaded-at metadata.
// It is display and settlement evidence, not a measurement: a controller
// that sends an empty, numeric or malformed value must not fail every
// Mihomo check just because this field was added.
func providerUpdatedAt(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}
	}
	return at
}
