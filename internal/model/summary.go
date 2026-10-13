package model

import (
	"sort"
	"unicode/utf8"
)

func SummaryText(s string) string {
	if len(s) <= 256 {
		return s
	}
	s = s[:253]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "..."
}

// SummaryObservation leaves full evidence in history and bounds diagnostics in
// the frequently refreshed project summary. Map ownership never leaks to callers.
func SummaryObservation(v Observation) Observation {
	v.Detail = SummaryText(v.Detail)
	keys := make([]string, 0, len(v.Facts))
	for k := range v.Facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	facts := map[string]string{}
	budget := 2048
	if proof, ok := v.Facts["response_proof"]; ok {
		facts["response_proof"] = SummaryText(proof)
		budget -= len(facts["response_proof"]) + 32
	}
	for _, k := range keys {
		val := SummaryText(v.Facts[k])
		if len(k)+len(val) > budget {
			continue
		}
		facts[k] = val
		budget -= len(k) + len(val)
	}
	v.Facts = facts
	timings := map[string]int64{}
	for k, n := range v.Timings {
		timings[k] = n
	}
	v.Timings = timings
	return v
}
