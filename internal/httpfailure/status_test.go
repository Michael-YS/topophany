package httpfailure

import "testing"

func TestHTTPFailureClassification(t *testing.T) {
	for status := 500; status <= 599; status++ {
		if Classify(status) != Transient {
			t.Fatal(status)
		}
	}
	for _, tc := range []struct {
		status int
		kind   Kind
	}{{401, Authentication}, {403, Permission}, {408, Transient}, {429, RateLimited}, {422, Rejected}, {302, Other}, {200, Other}} {
		if Classify(tc.status) != tc.kind {
			t.Fatal(tc)
		}
	}
}
