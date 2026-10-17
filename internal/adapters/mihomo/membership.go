package mihomo

import "slices"

type membershipComparison struct {
	matches        bool
	missing, extra int
	orderChanged   bool
}

func compareMembers(actual, want []string, mode string) membershipComparison {
	desired, loaded := map[string]int{}, map[string]int{}
	for _, name := range want {
		desired[name]++
	}
	for _, name := range actual {
		loaded[name]++
	}
	result := membershipComparison{matches: slices.Equal(actual, want)}
	for name, count := range desired {
		result.missing += max(0, count-loaded[name])
	}
	for name, count := range loaded {
		result.extra += max(0, count-desired[name])
	}
	result.orderChanged = result.missing == 0 && result.extra == 0 && !result.matches
	if mode == "set" {
		result.matches = result.missing == 0 && result.extra == 0 && len(loaded) == len(actual) && len(desired) == len(want)
	}
	return result
}
