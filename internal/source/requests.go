package source

import (
	"slices"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// RequestsForChecks groups enabled bindings by upstream object. One resource
// and one status binding may share a read; duplicate declarations never create
// additional network work. Callers select the source's checks first.
func RequestsForChecks(checks []model.Check) []Request {
	requests := []Request{}
	index := map[string]int{}
	for _, x := range checks {
		if x.External == nil || x.Disabled || x.Maintenance {
			continue
		}
		object, kind := x.External.Object, x.External.Report
		if i, ok := index[object]; ok {
			if !slices.Contains(requests[i].Reports, kind) {
				requests[i].Reports = append(requests[i].Reports, kind)
			}
		} else {
			index[object] = len(requests)
			requests = append(requests, Request{Object: object, Reports: []string{kind}})
		}
	}
	return requests
}
