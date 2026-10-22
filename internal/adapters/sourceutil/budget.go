package sourceutil

import (
	"errors"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// MaxResponseBytes is the common per-response ceiling for external sources.
const MaxResponseBytes = 8 << 20

// ObjectBudget returns a closed failure code, or an empty string on success.
// Adapters retain object identity, uniqueness and report-type validation.
func ObjectBudget(s model.Source, count int) string {
	if count < 1 || s.MaxObjects < 1 || s.MaxObjects > model.MaxSourceObjects || count > s.MaxObjects {
		return "object_budget"
	}
	return ""
}

// ReadBudget checks the complete vendor-planned read count before obtaining
// any credential or starting a request. The byte budget is per response.
func ReadBudget(s model.Source, usedPages, plannedPages int) string {
	if s.MaxPages < 1 || s.MaxPages > model.MaxSourcePages || usedPages < 0 || usedPages > s.MaxPages || plannedPages < 1 || plannedPages > s.MaxPages-usedPages {
		return "page_budget"
	}
	if s.MaxResponseBytes < 1 || s.MaxResponseBytes > MaxResponseBytes {
		return "response_budget"
	}
	return ""
}

// JSONBudget preserves exact Unicode and checks structural cost before vendor
// decoding. Each protocol retains its own member, array and container limits.
func JSONBudget(body []byte, members, array, containers int) error {
	if len(body) > MaxResponseBytes || !secure.ValidJSONUnicode(body) || secure.JSONResponseBudget(body, members, array, containers) != nil {
		return errors.New("source JSON representation exceeds its budget")
	}
	return nil
}
