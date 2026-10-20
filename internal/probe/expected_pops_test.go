package probe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A response that reports a colo outside expected_pops is a policy sample
// after a passing request, stage edge_assert, never a failed request. A
// response that reports none, including one withheld because the request
// carried a secret, asserts nothing.
func TestHTTPExpectedPopsPolicy(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hkg":
			w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-HKG")
		case "/lax":
			w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-LAX")
		case "/vercel":
			w.Header().Set("X-Vercel-Id", "hnd1::8f2c1b2a3d4e5f60")
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	run := func(path string, pops []string, secret bool) model.Observation {
		x := httpCheck(s.URL + path)
		x.HTTP.ExpectedPops = pops
		if secret {
			x.HTTP.RequestHeaders = map[string]string{"X-Key": "test-key"}
		}
		return (Runner{}).Run(context.Background(), testkit.Config(), x)
	}
	if o := run("/hkg", []string{"HKG", "NRT"}, false); o.Outcome != "ok" || o.Facts["edge_pop"] != "HKG" {
		t.Fatalf("a declared colo: %+v", o)
	}
	o := run("/lax", []string{"HKG", "NRT"}, false)
	if o.Outcome != "policy" || o.Stage != "edge_assert" || o.Code != "pop_mismatch" || !o.Connectivity || o.Detail != "request succeeded but the edge answered from a colo outside the declared list" {
		t.Fatalf("a colo outside the list: %+v", o)
	}
	if o.Facts["edge_pop"] != "LAX" {
		t.Fatal("the observed colo was not kept on a policy sample", o.Facts)
	}
	if o := run("/", []string{"HKG"}, false); o.Outcome != "ok" || o.Facts["edge_pop"] != "" {
		t.Fatalf("no edge named a colo: %+v", o)
	}
	// A Vercel region is not a colo, so it neither matches nor violates.
	if o := run("/vercel", []string{"HND"}, false); o.Outcome != "ok" || o.Facts["edge_provider"] != "vercel" || o.Facts["edge_pop"] != "" {
		t.Fatalf("a Vercel region decided the colo policy: %+v", o)
	}
	// The request is secret, so the colo is withheld; the policy cannot
	// compare what it must not store, and the sample passes.
	o = run("/hkg", []string{"LAX"}, true)
	if o.Outcome != "ok" || o.Facts["edge_pop"] != "" || o.Facts["peer_text"] != withheld {
		t.Fatalf("a withheld colo was compared: %+v", o)
	}
}

// The order is ordinary assertions, expected_exit, expected_pops, then
// expected_trace: each earlier policy decides first.
func TestExpectedPopsRunsAfterAssertionsAndBeforeTrace(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-LAX")
		if r.URL.Path == "/trace" {
			_, _ = io.WriteString(w, "fl=abc\ncolo=NRT\nip=203.0.113.8\n")
			return
		}
		_, _ = io.WriteString(w, "192.0.2.1")
	}))
	defer s.Close()
	// expected_exit fails before the colo policy is reached.
	x := httpCheck(s.URL + "/plain")
	x.HTTP.ExpectedExit = []string{"203.0.113.8"}
	x.HTTP.ExpectedPops = []string{"HKG"}
	if o := (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "policy" || o.Code != "exit_mismatch" {
		t.Fatalf("exit policy did not run first: %+v", o)
	}
	// expected_pops fails before expected_trace compares its colo.
	x = httpCheck(s.URL + "/trace")
	x.HTTP.ExitFormat = "trace"
	x.HTTP.ExpectedPops = []string{"HKG"}
	x.HTTP.ExpectedTrace = &model.TraceExpectations{Colos: []string{"HKG"}}
	if o := (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "policy" || o.Stage != "edge_assert" || o.Code != "pop_mismatch" {
		t.Fatalf("pop policy did not run before the trace policy: %+v", o)
	}
	// An ordinary assertion failing first keeps its own code.
	x = httpCheck(s.URL + "/plain")
	x.HTTP.Status = []int{204}
	x.HTTP.ExpectedPops = []string{"HKG"}
	if o := (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "fail" || o.Code != "unexpected_status" {
		t.Fatalf("an assertion failure became a pop policy: %+v", o)
	}
}

// popOutside is the decision itself: the list must be declared, the edge
// must have reported a colo, Vercel's reported region is not a colo, and
// the reported colo must not be in the list.
func TestPopOutside(t *testing.T) {
	for _, tc := range []struct {
		name     string
		facts    map[string]string
		declared []string
		want     bool
	}{
		{"match", map[string]string{"edge_provider": "cloudflare", "edge_pop": "HKG"}, []string{"HKG", "NRT"}, false},
		{"mismatch", map[string]string{"edge_provider": "cloudflare", "edge_pop": "LAX"}, []string{"HKG", "NRT"}, true},
		{"absent", map[string]string{"edge_provider": "cloudflare"}, []string{"HKG"}, false},
		{"no list", map[string]string{"edge_pop": "LAX"}, nil, false},
		{"vercel", map[string]string{"edge_provider": "vercel", "edge_pop": "hnd1"}, []string{"HND", "NRT"}, false},
		{"no provider", map[string]string{"edge_pop": "LAX"}, []string{"HKG"}, true},
	} {
		if got := popOutside(tc.facts, tc.declared); got != tc.want {
			t.Errorf("%s: popOutside = %v, want %v", tc.name, got, tc.want)
		}
	}
}
