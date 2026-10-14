package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func httpCheck(url string) model.Check {
	return model.Check{ID: "check", Observer: "home", Kind: "http", Target: url, TimeoutSeconds: 1, HTTP: model.HTTPOptions{Method: "GET", MaxBodyBytes: 65536}}
}
func TestHTTPAssertionsAndExitPolicy(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		options       model.HTTPOptions
		outcome, code string
	}{
		{"status-only", 200, "ok", model.HTTPOptions{}, "ok", ""},
		{"expected-unauthenticated-401", 401, "unauthorized", model.HTTPOptions{Status: []int{401}}, "ok", ""},
		{"unexpected-status", 503, "service unavailable", model.HTTPOptions{}, "fail", "unexpected_status"},
		{"contains", 200, "prefix READY suffix", model.HTTPOptions{Contains: "READY"}, "ok", ""},
		{"wrong-body", 200, "not ready", model.HTTPOptions{Contains: "READY"}, "fail", "body_mismatch"},
		{"regex", 200, "version=12", model.HTTPOptions{BodyRegex: `^version=\d+$`}, "ok", ""},
		{"wrong-regex", 200, "version=oops", model.HTTPOptions{BodyRegex: `^version=\d+$`}, "fail", "regex_mismatch"},
		{"header", 200, "ok", model.HTTPOptions{Headers: map[string]string{"X-Health": "ready"}}, "ok", ""},
		{"wrong-header", 200, "ok", model.HTTPOptions{Headers: map[string]string{"X-Health": "other"}}, "fail", "header_mismatch"},
		{"plain-exit", 200, "203.0.113.8\n", model.HTTPOptions{ExpectedExit: []string{"203.0.113.8"}}, "ok", ""},
		{"CIDR-exit", 200, "203.0.113.8", model.HTTPOptions{ExpectedExit: []string{"203.0.113.0/24"}}, "ok", ""},
		{"wrong-residential-exit", 200, "192.0.2.1", model.HTTPOptions{ExpectedExit: []string{"203.0.113.8"}}, "policy", "exit_mismatch"},
		{"trace-exit", 200, "fl=abc\nip=203.0.113.8\n", model.HTTPOptions{ExpectedExit: []string{"203.0.113.8"}, ExitFormat: "trace"}, "ok", ""},
		{"JSON-exit", 200, `{"ip":"203.0.113.8"}`, model.HTTPOptions{ExpectedExit: []string{"203.0.113.8"}, ExitFormat: "json"}, "ok", ""},
		{"custom-JSON-exit", 200, `{"address":"203.0.113.8"}`, model.HTTPOptions{ExpectedExit: []string{"203.0.113.8"}, ExitFormat: "json", ExitJSONKey: "address"}, "ok", ""},
		{"malformed-exit", 200, "not an IP", model.HTTPOptions{ExpectedExit: []string{"203.0.113.8"}}, "fail", "exit_not_observed"},
		{"missing-JSON-exit", 200, `{"other":1}`, model.HTTPOptions{ExpectedExit: []string{"203.0.113.8"}, ExitFormat: "json"}, "fail", "exit_not_observed"},
		{"bounded-response-is-monitor-limit", 200, strings.Repeat("x", 32), model.HTTPOptions{MaxBodyBytes: 16}, "error", "body_budget_exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Health", "ready")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer s.Close()
			x := httpCheck(s.URL)
			x.HTTP = tt.options
			x.HTTP.Method = "GET"
			if x.HTTP.MaxBodyBytes == 0 {
				x.HTTP.MaxBodyBytes = 65536
			}
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.outcome || o.Code != tt.code {
				t.Fatalf("got %+v", o)
			}
			if !o.Connectivity {
				t.Fatal("real HTTP response should retain response evidence even on assertion failure")
			}
		})
	}
}
func TestHTTPSendsTheConfiguredHost(t *testing.T) {
	hosts := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hosts <- r.Host }))
	defer srv.Close()
	for _, key := range []string{"Host", "host"} {
		x := httpCheck(srv.URL + "/")
		x.HTTP.RequestHeaders = map[string]string{key: "blog.example.invalid"}
		if o := (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "ok" {
			t.Fatal(o)
		}
		if got := <-hosts; got != "blog.example.invalid" {
			t.Fatalf("%s header: server saw Host %q", key, got)
		}
	}
}
func TestHTTPRedirectNeverForwardsCredentials(t *testing.T) {
	t.Setenv("TOPH_TEST_TOKEN", "secret-bearer-value")
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-bearer-value" {
			t.Error("target bearer missing")
		}
		http.Redirect(w, r, target.URL, 302)
	}))
	defer source.Close()
	x := httpCheck(source.URL)
	x.HTTP.BearerEnv = "TOPH_TEST_TOKEN"
	x.HTTP.Status = []int{302}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || reached.Load() != 0 {
		t.Fatal("redirect followed", o)
	}
	b, _ := json.Marshal(o)
	if strings.Contains(string(b), "secret-bearer-value") {
		t.Fatal("observation leaked bearer")
	}
}
func TestHTTPMissingSecretAndNoAmbientProxy(t *testing.T) {
	t.Setenv("TOPH_MISSING", "")
	x := httpCheck("http://127.0.0.1:9/")
	x.HTTP.BearerEnv = "TOPH_MISSING"
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || o.Code != "http_secret_missing" {
		t.Fatal(o)
	}
	var trapped atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trapped.Add(1); w.WriteHeader(502) }))
	defer trap.Close()
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY"} {
		t.Setenv(key, trap.URL)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "monitor.example.test:") {
			t.Error("Host header replaced by pinned address")
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	x = httpCheck("http://monitor.example.test:" + port + "/")
	x.ResolveIP = "127.0.0.1"
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || trapped.Load() != 0 {
		t.Fatal("ambient proxy hijacked explicit probe", o)
	}
}
func tlsFixture(t *testing.T, handler http.Handler) (*httptest.Server, string) {
	t.Helper()
	cert, key, ca := testkit.Certificate(t, "probe.example.test")
	pair, e := tls.LoadX509KeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(handler)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s, ca
}
func TestTLSVerificationAndPinnedDialAddress(t *testing.T) {
	s, ca := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" {
			t.Error("empty Host")
		}
		_, _ = io.WriteString(w, "ready")
	}))
	x := httpCheck(s.URL)
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "fail" || o.Stage != "tls" {
		t.Fatal("untrusted certificate accepted", o)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "https://"))
	x.Target = "https://probe.example.test:" + port + "/"
	x.ResolveIP = "127.0.0.1"
	x.CAFile = ca
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["response_proof"] != "verified_https" {
		t.Fatal(o)
	}
	x.ServerName = "wrong.example.test"
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "fail" || o.Stage != "tls" {
		t.Fatal("wrong SNI name accepted", o)
	}
	x.ServerName = ""
	x.CAFile = filepath.Join(t.TempDir(), "missing")
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" {
		t.Fatal("missing CA file called service outage", o)
	}
	bad := filepath.Join(t.TempDir(), "bad-ca")
	_ = os.WriteFile(bad, []byte("not a certificate"), 0600)
	x.CAFile = bad
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Code != "ca_file_invalid" {
		t.Fatal(o)
	}
}
func TestHTTPNewClientStreamAndCancellation(t *testing.T) {
	var connections atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	s.Start()
	defer s.Close()
	x := httpCheck(s.URL)
	for i := 0; i < 2; i++ {
		o := (Runner{}).Run(context.Background(), testkit.Config(), x)
		if o.Outcome != "ok" || o.Facts["client_connection_reused"] != "false" || !strings.Contains(o.Facts["connection_semantics"], "upstream_core_multiplexing_unknown") {
			t.Fatal(o)
		}
	}
	if connections.Load() != 2 {
		t.Fatal("new request reused previous connection")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	began := time.Now()
	o := (Runner{}).Run(ctx, testkit.Config(), httpCheck(slow.URL))
	if time.Since(began) > 500*time.Millisecond || o.Outcome != "fail" {
		t.Fatal("cancellation not bounded", o)
	}
	x = httpCheck(slow.URL)
	began = time.Now()
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Code != "timeout" || time.Since(began) > 2*time.Second {
		t.Fatal("deadline not enforced", o)
	}
}
func TestStreamEvidenceIsProtocolSpecific(t *testing.T) {
	for _, tt := range []struct{ name, kind, response, outcome, proof string }{{"TCP local accept", "tcp", "", "ok", "none"}, {"SSH banner", "ssh-banner", "notice\r\nSSH-2.0-Topophany-test\r\n", "ok", "ssh_banner"}, {"invalid SSH banner", "ssh-banner", strings.Repeat("x", 300) + "\n", "fail", ""}} {
		t.Run(tt.name, func(t *testing.T) {
			addr := testkit.Stream(t, func(c net.Conn) {
				if tt.response != "" {
					_, _ = io.WriteString(c, tt.response)
				} else {
					_, _ = io.Copy(io.Discard, c)
				}
			})
			x := httpCheck("")
			x.Kind = tt.kind
			x.Target = addr
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tt.outcome || o.Facts["response_proof"] != tt.proof {
				t.Fatal(o)
			}
		})
	}
	s, ca := tlsFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	x := httpCheck("")
	x.Kind = "tls"
	x.Target = strings.TrimPrefix(s.URL, "https://")
	x.CAFile = ca
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["response_proof"] != "verified_tls" {
		t.Fatal(o)
	}
}
func TestDisabledAndUnsupportedAreNotInventedMeasurements(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	x.Disabled = true
	o := (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "unknown" || o.Connectivity {
		t.Fatal(o)
	}
	x.Disabled = false
	x.Kind = "made-up"
	o = (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "error" || o.Code != "unsupported_check" {
		t.Fatal(o)
	}
	_, _, code := classify(errors.New("secret raw error text"))
	if strings.Contains(code, "secret") {
		t.Fatal("raw error leaked")
	}
	if config.Revision(c) == "" {
		t.Fatal("missing contract revision")
	}
}

// An edge in front of an origin answers for it, even when the origin is
// gone: the probe keeps which edge, where and whether from its cache.
func TestHTTPRecordsTheEdgeThatAnswered(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		w.Header().Set("Cf-Cache-Status", "dynamic")
		w.WriteHeader(521)
		_, _ = io.WriteString(w, "web server is down")
	}))
	defer s.Close()
	o := (Runner{}).Run(context.Background(), testkit.Config(), httpCheck(s.URL))
	if o.Outcome != "fail" || o.Code != "unexpected_status" || !o.Connectivity || o.Facts["http_status"] != "521" {
		t.Fatalf("edge error page: %+v", o)
	}
	if o.Facts["edge_provider"] != "cloudflare" || o.Facts["edge_pop"] != "NRT" || o.Facts["edge_cache"] != "DYNAMIC" {
		t.Fatal("edge facts", o.Facts)
	}
}

// An HTTP peer sent a bearer token can hand it back in the headers an
// edge's labels are read from, or as the exit address. The observation,
// which every viewer of a collector reads, keeps the edge's name and a
// cache status from a closed list, and checks the exit without keeping
// it, so no part of the token reaches it. Sent no secret, the labels and
// the exit are kept.
func TestHTTPSentASecretCannotEchoIt(t *testing.T) {
	const token = "tok-a1b2c3d4e5f6a7b8c9d0e1f2"
	t.Setenv("TOPH_HTTP_TOKEN", token)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Vercel-Id", token[:12]+"::"+token[12:]+"::x")
		w.Header().Set("X-Vercel-Cache", token[12:])
		_, _ = io.WriteString(w, "2001:db8:a1b2:c3d4:e5f6:a7b8:c9d0:e1f2")
	}))
	defer s.Close()
	for _, tt := range []struct {
		name    string
		check   func(*model.Check)
		secret  bool
		pop, ip string
	}{
		{"bearer", func(x *model.Check) { x.HTTP.BearerEnv = "TOPH_HTTP_TOKEN" }, true, "", ""},
		{"private header", func(x *model.Check) { x.HTTP.RequestHeaders = map[string]string{"X-Key": token} }, true, "", ""},
		{"host header", func(x *model.Check) { x.HTTP.RequestHeaders = map[string]string{"Host": "example.invalid"} }, false, token[:12], "2001:db8:a1b2:c3d4:e5f6:a7b8:c9d0:e1f2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x := httpCheck(s.URL)
			x.HTTP.ExpectedExit = []string{"2001:db8::/32"}
			tt.check(&x)
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != "ok" || o.Facts["edge_provider"] != "vercel" || o.Facts["edge_pop"] != tt.pop || o.Facts["exit_ip"] != tt.ip || o.Facts["edge_cache"] != "" || (o.Facts["peer_text"] == withheld) != tt.secret {
				t.Fatal(o.Outcome, o.Facts)
			}
			if !tt.secret {
				return
			}
			b, _ := json.Marshal(o)
			for _, part := range []string{token[:12], token[12:], "a1b2", "A1B2"} {
				if strings.Contains(string(b), part) {
					t.Fatalf("observation carries %q: %s", part, b)
				}
			}
		})
	}
}

func TestEdgeFactsFromEachNetworkAndUntrustedValues(t *testing.T) {
	header := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Add(kv[i], kv[i+1])
		}
		return h
	}
	for _, tt := range []struct {
		h                             http.Header
		provider, pop, region, cached string
	}{
		{header("Cf-Ray", "8f2c1b2a3d4e5f60-LAX", "Cf-Cache-Status", "HIT"), "cloudflare", "LAX", "", "HIT"},
		{header("X-Vercel-Id", "hnd1::iad1::abcd-1700000000000-0123456789ab", "X-Vercel-Cache", "MISS"), "vercel", "hnd1", "iad1", "MISS"},
		{header("X-Vercel-Id", "sin1::abcd-1700000000000"), "vercel", "sin1", "", ""},
		{header("X-Amz-Cf-Pop", "NRT57-P1", "X-Cache", "Hit from cloudfront"), "cloudfront", "NRT57-P1", "", "HIT"},
		{header("X-Served-By", "cache-iad-kiad7000025-IAD, cache-nrt-rjtf7700040-NRT", "X-Cache", "MISS, HIT"), "fastly", "NRT", "", "HIT"},
		{header("Server", "Netlify", "Cache-Status", `"Netlify Durable"; fwd=bypass, "Netlify Edge"; hit`), "netlify", "", "", "HIT"},
		{header("Server", "nginx"), "", "", "", ""},
		// A cache status is one edges have, or none.
		{header("Cf-Ray", "8f2c1b2a3d4e5f60-LAX", "Cf-Cache-Status", "secret-token"), "cloudflare", "LAX", "", ""},
		{header("X-Amz-Cf-Pop", "NRT57-P1", "X-Cache", "LambdaGeneratedResponse from cloudfront"), "cloudfront", "NRT57-P1", "", "LAMBDAGENERATEDRESPONSE"},
		// Header values are untrusted: kept short and free of anything
		// that is not a label character.
		{header("Cf-Ray", "x-NRT\x1b[2J"+strings.Repeat("A", 64), "Cf-Cache-Status", "hit\r\n"), "cloudflare", "NRT2J" + strings.Repeat("A", 19), "", "HIT"},
	} {
		f := EdgeFacts(tt.h)
		if f["edge_provider"] != tt.provider || f["edge_pop"] != tt.pop || f["edge_region"] != tt.region || f["edge_cache"] != tt.cached {
			t.Fatalf("%v: %v", tt.h, f)
		}
	}
}
