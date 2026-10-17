package mihomo

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestMihomoMembershipModesAndDuplicateSafety(t *testing.T) {
	for _, tc := range []struct {
		actual, want   []string
		mode           string
		match          bool
		missing, extra int
	}{
		{[]string{"b", "a"}, []string{"a", "b"}, "", false, 0, 0},
		{[]string{"b", "a"}, []string{"a", "b"}, "set", true, 0, 0},
		{[]string{"a", "a"}, []string{"a", "b"}, "set", false, 1, 1},
		{[]string{"a"}, []string{"a", "b"}, "set", false, 1, 0},
	} {
		r := compareMembers(tc.actual, tc.want, tc.mode)
		if r.matches != tc.match || r.missing != tc.missing || r.extra != tc.extra {
			t.Fatal(r)
		}
	}
	for _, modes := range []map[string]string{{"unknown": "set"}, {"pool": "invalid"}} {
		if Validate(model.MihomoOptions{ExpectedProviders: map[string][]string{"pool": {"a"}}, ProviderMatchModes: modes}) == nil {
			t.Fatal(modes)
		}
	}
	if Validate(model.MihomoOptions{ExpectedProviders: map[string][]string{"pool": {"a", "a"}}, ProviderMatchModes: map[string]string{"pool": "set"}}) == nil {
		t.Fatal("duplicate set expectations accepted")
	}
}
