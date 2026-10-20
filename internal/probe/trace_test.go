package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const traceBody = `fl=445f244
h=www.cloudflare.com
ip=203.0.113.8
ts=1792451196.000
visit_scheme=https
uag=curl/8.7.1
colo=HKG
sliver=050-tier1
http=http/1.1
loc=JP
tls=TLSv1.3
sni=plaintext
warp=on
gateway=off
rbi=off
kex=X25519
`

func traceServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace-Test", "ready")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func traceObservation(t *testing.T, s *httptest.Server, path string, options model.HTTPOptions) model.Observation {
	t.Helper()
	x := httpCheck(s.URL + path)
	x.HTTP = options
	x.HTTP.Method = "GET"
	if x.HTTP.MaxBodyBytes == 0 {
		x.HTTP.MaxBodyBytes = 65536
	}
	return (Runner{}).Run(context.Background(), testkit.Config(), x)
}

// A trace check turns the edge's own account of the request into facts: the
// colo, the WARP and Gateway states, the negotiated versions and the client
// country, plus the egress address from the ip= line even without an
// expected_exit assertion. The hostnames, the agent string and the remaining
// lines are not kept.
func TestHTTPReadsCloudflareTrace(t *testing.T) {
	s := traceServer(t, 200, traceBody)
	o := traceObservation(t, s, "/cdn-cgi/trace", model.HTTPOptions{ExitFormat: "trace"})
	if o.Outcome != "ok" || !o.Connectivity || o.Facts["peer_text"] != "" {
		t.Fatalf("trace check: %+v", o)
	}
	want := map[string]string{
		"trace_colo": "HKG", "trace_warp": "on", "trace_gateway": "off",
		"trace_http": "http1.1", "trace_tls": "TLSv1.3", "trace_loc": "JP",
		"exit_ip": "203.0.113.8",
	}
	for k, v := range want {
		if o.Facts[k] != v {
			t.Fatalf("%s = %q; want %q in %#v", k, o.Facts[k], v, o.Facts)
		}
	}
	for _, k := range []string{"uag", "h", "sni", "ts", "fl", "visit_scheme", "sliver", "kex", "rbi", "ip", "colo", "loc"} {
		if _, ok := o.Facts[k]; ok {
			t.Fatalf("trace line %q was kept as a fact: %#v", k, o.Facts)
		}
	}
}

// A query makes the request private. What the edge answered then keeps only
// the closed WARP, Gateway, HTTP and TLS words: the colo, the client country
// and the egress address are free labels and are withheld, as the peer could
// have reflected the query into them.
func TestHTTPTraceWithheldOnSecretRequest(t *testing.T) {
	s := traceServer(t, 200, traceBody)
	o := traceObservation(t, s, "/cdn-cgi/trace?key=secret", model.HTTPOptions{ExitFormat: "trace"})
	if o.Outcome != "ok" || o.Facts["peer_text"] != withheld {
		t.Fatalf("secret trace check: %+v", o)
	}
	for k, v := range map[string]string{"trace_warp": "on", "trace_gateway": "off", "trace_http": "http1.1", "trace_tls": "TLSv1.3"} {
		if o.Facts[k] != v {
			t.Fatalf("closed trace fact %s = %q; want %q", k, o.Facts[k], v)
		}
	}
	for _, k := range []string{"trace_colo", "trace_loc", "exit_ip", "http_status"} {
		if _, ok := o.Facts[k]; ok {
			t.Fatalf("free or secret fact %q survived: %#v", k, o.Facts)
		}
	}
}

// A peer sent a secret can echo it into every free field of a trace body: the
// colo, the client country and the agent string can all be built from the
// query. None of that reaches the observation, while the four closed words
// survive.
func TestHTTPTraceSecretCannotEcho(t *testing.T) {
	const secret = "XYZQR123"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		_, _ = io.WriteString(w, "fl=x\ncolo="+key[:3]+"\nloc="+key[3:5]+
			"\nwarp=on\ngateway=off\nhttp=http/1.1\ntls=TLSv1.3\nuag="+key+
			"\nh="+key+".example\nsni="+key+"\nip=203.0.113.8\n")
	}))
	t.Cleanup(s.Close)
	x := httpCheck(s.URL + "/cdn-cgi/trace?key=" + secret)
	x.HTTP.ExitFormat = "trace"
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["peer_text"] != withheld {
		t.Fatalf("secret trace check: %+v", o)
	}
	for k, v := range map[string]string{"trace_warp": "on", "trace_gateway": "off", "trace_http": "http1.1", "trace_tls": "TLSv1.3"} {
		if o.Facts[k] != v {
			t.Fatalf("closed trace fact %s = %q; want %q", k, o.Facts[k], v)
		}
	}
	for _, k := range []string{"trace_colo", "trace_loc", "exit_ip", "uag", "h", "sni"} {
		if _, ok := o.Facts[k]; ok {
			t.Fatalf("free fact %q survived: %#v", k, o.Facts)
		}
	}
	raw, _ := json.Marshal(o)
	for _, part := range []string{"XYZ", "QR", secret, "203.0.113.8"} {
		if strings.Contains(string(raw), part) {
			t.Fatalf("observation carries %q: %s", part, raw)
		}
	}
}

// Without an expected_exit assertion the address is recorded when the body
// reports it, and a body that reports none is not a failure.
func TestHTTPTraceExitAddressWithoutAssertion(t *testing.T) {
	withoutIP := strings.Replace(traceBody, "ip=203.0.113.8\n", "", 1)
	s := traceServer(t, 200, withoutIP)
	o := traceObservation(t, s, "/cdn-cgi/trace", model.HTTPOptions{ExitFormat: "trace"})
	if o.Outcome != "ok" || o.Facts["exit_ip"] != "" || o.Facts["trace_colo"] != "HKG" {
		t.Fatalf("missing ip line: %+v", o)
	}
}

// The ip= line belongs to the trace format: a plain body is read as before,
// without an address or trace facts.
func TestHTTPTraceOnlyForTraceFormat(t *testing.T) {
	s := traceServer(t, 200, traceBody)
	o := traceObservation(t, s, "/cdn-cgi/trace", model.HTTPOptions{ExitFormat: "plain"})
	if o.Outcome != "ok" || o.Facts["exit_ip"] != "" || o.Facts["trace_colo"] != "" || o.Facts["trace_warp"] != "" {
		t.Fatalf("plain format read a trace body: %+v", o)
	}
}

// The 4 KiB cap governs every trace reading, including the unconditional
// exit_ip; a larger body is not a trace this probe reads. expected_exit keeps
// its own exit parser, exactly as before, so a declared address is still
// compared.
func TestHTTPTraceOverByteLimit(t *testing.T) {
	body := traceBody + strings.Repeat("x\n", 2100)
	if len(body) <= 4096 {
		t.Fatal(len(body))
	}
	s := traceServer(t, 200, body)
	o := traceObservation(t, s, "/cdn-cgi/trace", model.HTTPOptions{ExitFormat: "trace"})
	if o.Outcome != "ok" || o.Facts["trace_colo"] != "" || o.Facts["trace_warp"] != "" || o.Facts["exit_ip"] != "" {
		t.Fatalf("a body over 4 KiB produced trace evidence: %+v", o)
	}
	o = traceObservation(t, s, "/cdn-cgi/trace", model.HTTPOptions{
		ExitFormat:   "trace",
		ExpectedExit: []string{"203.0.113.8"},
	})
	if o.Outcome != "ok" || o.Facts["exit_ip"] != "203.0.113.8" || o.Facts["trace_colo"] != "" {
		t.Fatalf("expected_exit did not read its own parser: %+v", o)
	}
}

// expected_trace compares the edge's account after the request has passed its
// other assertions. A different reported WARP, Gateway or colo is a policy
// sample, stage trace_assert, with a detail that names neither the observed
// colo nor any free text. A line the trace did not report is not a violation.
func TestHTTPExpectedTracePolicy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		options    model.HTTPOptions
		path       string
		outcome    string
		stage      string
		code       string
		detail     []string
		notDetail  []string
		facts      map[string]string
		absentFact []string
	}{
		{
			name: "warp mismatch", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "off"}},
			outcome: "policy", stage: "trace_assert", code: "warp_mismatch",
		},
		{
			name: "gateway mismatch", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Gateway: "on"}},
			outcome: "policy", stage: "trace_assert", code: "gateway_mismatch",
		},
		{
			name: "colo outside the list", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Colos: []string{"NRT", "LAX"}}},
			outcome: "policy", stage: "trace_assert", code: "colo_mismatch",
			notDetail: []string{"HKG"},
		},
		{
			name: "all expectations pass", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "on", Gateway: "off", Colos: []string{"HKG", "NRT"}}},
			outcome: "ok", stage: "http_assert", detail: []string{"result applies only to this request"},
			facts: map[string]string{"trace_colo": "HKG"},
		},
		{
			name: "warp wins over gateway", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "off", Gateway: "on"}},
			outcome: "policy", stage: "trace_assert", code: "warp_mismatch",
		},
		{
			name: "missing warp is not a violation", body: strings.Replace(traceBody, "warp=on\n", "", 1),
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "on"}},
			outcome: "ok", stage: "http_assert", detail: []string{"did not report warp"},
		},
		{
			name: "missing gateway is not a violation", body: strings.Replace(traceBody, "gateway=off\n", "", 1),
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Gateway: "off"}},
			outcome: "ok", stage: "http_assert", detail: []string{"did not report gateway"},
		},
		{
			name: "an empty colo is not a violation", body: strings.Replace(traceBody, "colo=HKG\n", "colo=\n", 1),
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Colos: []string{"HKG"}}},
			outcome: "ok", stage: "http_assert", detail: []string{"did not report colo"},
		},
		{
			name: "a body that is not a trace is named as such", body: "not a trace\n",
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "on", Gateway: "off", Colos: []string{"HKG"}}},
			outcome: "ok", stage: "http_assert", detail: []string{"the body was not a trace"}, notDetail: []string{"did not report"},
		},
		{
			name: "a secret request still compares the colo", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Colos: []string{"NRT"}}},
			path:    "/cdn-cgi/trace?key=secret",
			outcome: "policy", stage: "trace_assert", code: "colo_mismatch",
			notDetail:  []string{"HKG"},
			absentFact: []string{"trace_colo", "trace_loc", "exit_ip"},
			facts:      map[string]string{"trace_warp": "on", "trace_gateway": "off"},
		},
		{
			name: "a secret request passes a declared colo", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Colos: []string{"HKG", "NRT"}}},
			path:    "/cdn-cgi/trace?key=secret",
			outcome: "ok", stage: "http_assert",
			absentFact: []string{"trace_colo", "trace_loc", "exit_ip"},
		},
		{
			name: "a failing body substring stops before the trace policy", body: traceBody,
			options: model.HTTPOptions{Contains: "never in this body", ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "off"}},
			outcome: "fail", stage: "http_assert", code: "body_mismatch",
		},
		{
			name: "a failing body regex stops before the trace policy", body: traceBody,
			options: model.HTTPOptions{BodyRegex: "^nope$", ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "off"}},
			outcome: "fail", stage: "http_assert", code: "regex_mismatch",
		},
		{
			name: "a failing response header stops before the trace policy", body: traceBody,
			options: model.HTTPOptions{Headers: map[string]string{"X-Trace-Test": "wrong"}, ExitFormat: "trace", ExpectedTrace: &model.TraceExpectations{Warp: "off"}},
			outcome: "fail", stage: "http_assert", code: "header_mismatch",
		},
		{
			name: "a passing expected_exit gives way to the trace policy", body: traceBody,
			options: model.HTTPOptions{ExitFormat: "trace", ExpectedExit: []string{"203.0.113.0/24"}, ExpectedTrace: &model.TraceExpectations{Warp: "off"}},
			outcome: "policy", stage: "trace_assert", code: "warp_mismatch",
			facts: map[string]string{"exit_ip": "203.0.113.8", "trace_warp": "on"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := traceServer(t, 200, tc.body)
			path := tc.path
			if path == "" {
				path = "/cdn-cgi/trace"
			}
			o := traceObservation(t, s, path, tc.options)
			if o.Outcome != tc.outcome || o.Stage != tc.stage || o.Code != tc.code || !o.Connectivity {
				t.Fatalf("got %s/%s/%s %q; want %s/%s/%s", o.Outcome, o.Stage, o.Code, o.Detail, tc.outcome, tc.stage, tc.code)
			}
			for _, part := range tc.detail {
				if !strings.Contains(o.Detail, part) {
					t.Fatalf("detail %q does not name %q", o.Detail, part)
				}
			}
			for _, part := range tc.notDetail {
				if strings.Contains(o.Detail, part) {
					t.Fatalf("detail %q carries %q", o.Detail, part)
				}
			}
			if len(o.Detail) > 128 {
				t.Fatalf("detail is not bounded: %q", o.Detail)
			}
			for k, v := range tc.facts {
				if o.Facts[k] != v {
					t.Fatalf("%s = %q; want %q", k, o.Facts[k], v)
				}
			}
			for _, k := range tc.absentFact {
				if _, ok := o.Facts[k]; ok {
					t.Fatalf("fact %q survived a secret request: %#v", k, o.Facts)
				}
			}
		})
	}
}

// The trace expectation is evaluated only after the ordinary assertions and
// expected_exit have passed, so a failed request never reads as a policy
// sample of a different stage and a bad exit keeps its own code.
func TestHTTPTracePolicyRunsAfterAssertions(t *testing.T) {
	s := traceServer(t, 503, traceBody)
	o := traceObservation(t, s, "/cdn-cgi/trace", model.HTTPOptions{
		ExitFormat:    "trace",
		ExpectedTrace: &model.TraceExpectations{Warp: "off"},
	})
	if o.Outcome != "fail" || o.Stage != "http_assert" || o.Code != "unexpected_status" || o.Facts["trace_warp"] != "on" {
		t.Fatalf("assertion did not run first: %+v", o)
	}
	s = traceServer(t, 200, traceBody)
	o = traceObservation(t, s, "/cdn-cgi/trace", model.HTTPOptions{
		ExitFormat:    "trace",
		ExpectedExit:  []string{"192.0.2.1"},
		ExpectedTrace: &model.TraceExpectations{Warp: "off"},
	})
	if o.Outcome != "policy" || o.Stage != "exit_assert" || o.Code != "exit_mismatch" || o.Facts["exit_ip"] != "203.0.113.8" {
		t.Fatalf("exit assertion did not run first: %+v", o)
	}
}
