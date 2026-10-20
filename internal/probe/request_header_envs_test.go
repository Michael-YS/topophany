package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A header named by request_header_envs is sent with the value from the
// worker's environment, and the request counts as secret: free response labels
// are withheld and the observation says so.
func TestHTTPRequestHeaderEnvsSendAndWithhold(t *testing.T) {
	const (
		clientID     = "cf-access-client-id-1a2b3c4d"
		clientSecret = "cf-access-client-secret-5e6f7a8b"
	)
	t.Setenv("TOPH_CF_ID", clientID)
	t.Setenv("TOPH_CF_SECRET", clientSecret)
	var gotID, gotSecret atomic.Value
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID.Store(r.Header.Get("CF-Access-Client-Id"))
		gotSecret.Store(r.Header.Get("CF-Access-Client-Secret"))
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		w.Header().Set("Cf-Cache-Status", "HIT")
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.HTTP.RequestHeaderEnvs = map[string]string{
		"CF-Access-Client-Id":     "TOPH_CF_ID",
		"CF-Access-Client-Secret": "TOPH_CF_SECRET",
	}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || !o.Connectivity {
		t.Fatalf("secret header request: %+v", o)
	}
	if gotID.Load() != clientID || gotSecret.Load() != clientSecret {
		t.Fatalf("server saw headers %q %q", gotID.Load(), gotSecret.Load())
	}
	if o.Facts["edge_provider"] != "cloudflare" || o.Facts["edge_cache"] != "HIT" || o.Facts["peer_text"] != withheld {
		t.Fatal("closed edge facts or withholding", o.Facts)
	}
	if o.Facts["http_status"] != "" || o.Facts["edge_pop"] != "" {
		t.Fatal("free labels survived an environment-backed secret", o.Facts)
	}
	b, _ := json.Marshal(o)
	for _, secret := range []string{clientID, clientSecret} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("observation carries %q: %s", secret, b)
		}
	}
}

// A variable that is absent or empty is a local setup error: the check reports
// it as a monitor error and nothing is sent.
func TestHTTPRequestHeaderEnvMissingNeverDials(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer s.Close()
	t.Setenv("TOPH_PRESENT", "value")
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"unset", map[string]string{"X-Key": "TOPOPHANY_TEST_ABSENT_HEADER_ENV"}},
		{"empty", map[string]string{"X-Key": "TOPH_EMPTY"}},
		{"one of two", map[string]string{"X-Key": "TOPH_PRESENT", "X-Other": "TOPOPHANY_TEST_ABSENT_HEADER_ENV"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "empty" {
				t.Setenv("TOPH_EMPTY", "")
			}
			x := httpCheck(s.URL)
			x.HTTP.RequestHeaderEnvs = tc.env
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != "error" || o.Stage != "setup" || o.Code != "http_secret_missing" {
				t.Fatalf("missing environment variable: %+v", o)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("%d requests reached the server without a configured secret", hits.Load())
	}
}

// Host is allowed in the map, but its resolved value must still be a host or
// host:port; a bad one is a local header error, not a connect failure.
func TestHTTPRequestHeaderEnvSetsHost(t *testing.T) {
	hosts := make(chan string, 2)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hosts <- r.Host }))
	defer s.Close()
	t.Setenv("TOPH_VIRTUAL_HOST", "internal.example.test:8443")
	x := httpCheck(s.URL)
	x.HTTP.RequestHeaderEnvs = map[string]string{"Host": "TOPH_VIRTUAL_HOST"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["peer_text"] != withheld || o.Facts["http_status"] != "" {
		t.Fatalf("Host from the environment: %+v", o)
	}
	if got := <-hosts; got != "internal.example.test:8443" {
		t.Fatalf("server saw Host %q", got)
	}

	var hits atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer bad.Close()
	t.Setenv("TOPH_BAD_HOST", "internal.example.test/path")
	y := httpCheck(bad.URL)
	y.HTTP.RequestHeaderEnvs = map[string]string{"Host": "TOPH_BAD_HOST"}
	o = (Runner{}).Run(context.Background(), testkit.Config(), y)
	if o.Outcome != "error" || o.Stage != "setup" || o.Code != "invalid_http_headers" {
		t.Fatalf("invalid Host from the environment: %+v", o)
	}
	if hits.Load() != 0 {
		t.Fatal("invalid Host reached the server")
	}
}

// A peer sent an environment-backed header can reflect it through the edge
// labels and the Location it answers with. Closed words and numbers survive a
// secret request; the secret itself must not reach the observation.
func TestHTTPRequestHeaderEnvCannotEcho(t *testing.T) {
	const token = "cf-access-secret-9a8b7c6d5e4f"
	t.Setenv("TOPH_REFLECT_SECRET", token)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := r.Header.Get("X-Secret")
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-"+secret)
		w.Header().Set("Cf-Cache-Status", secret)
		w.Header().Set("Cf-Mitigated", "challenge"+secret)
		w.Header().Set("Location", "https://team.cloudflareaccess.com/cdn-cgi/access/login/host?kid="+secret)
		w.WriteHeader(http.StatusFound)
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.HTTP.Status = []int{http.StatusFound}
	x.HTTP.RequestHeaderEnvs = map[string]string{"X-Secret": "TOPH_REFLECT_SECRET"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["peer_text"] != withheld || o.Facts["edge_access"] != "login_redirect" || o.Facts["edge_provider"] != "cloudflare" {
		t.Fatalf("reflected secret request: %+v %v", o, o.Facts)
	}
	for _, key := range []string{"edge_pop", "edge_cache", "edge_mitigation", "http_status"} {
		if o.Facts[key] != "" {
			t.Fatalf("reflected value survived as %s: %q", key, o.Facts[key])
		}
	}
	b, _ := json.Marshal(o)
	for _, part := range []string{token[:12], token[12:], strings.ToUpper(token[:12])} {
		if strings.Contains(string(b), part) {
			t.Fatalf("observation carries %q: %s", part, b)
		}
	}
}

// A resolved value with a control byte would make net/http refuse the request
// after the probe decided to send. It is a local header error, sends nothing,
// and the value never reaches the observation.
func TestHTTPRequestHeaderEnvControlValueNeverDials(t *testing.T) {
	const value = "control-secret-9f8e\r\nInjected: x"
	t.Setenv("TOPH_CONTROL_SECRET", value)
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer s.Close()
	x := httpCheck(s.URL)
	x.HTTP.RequestHeaderEnvs = map[string]string{"X-Secret": "TOPH_CONTROL_SECRET"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || o.Stage != "setup" || o.Code != "invalid_http_headers" {
		t.Fatalf("control-bearing value: %+v", o)
	}
	if hits.Load() != 0 {
		t.Fatal("control-bearing value reached the server")
	}
	b, _ := json.Marshal(o)
	for _, part := range []string{"control-secret-9f8e", "Injected"} {
		if strings.Contains(string(b), part) {
			t.Fatalf("observation carries %q: %s", part, b)
		}
	}
}

// Synthetic loopback gate for D2's done-when. It is not a live Cloudflare
// Access host: it reproduces the documented shapes, a 302 login redirect
// unless both service-token headers match and a 200 when they do.
func TestHTTPSyntheticAccessServiceTokenGate(t *testing.T) {
	const (
		clientID     = "synthetic-access-id-4c1d"
		clientSecret = "synthetic-access-secret-8a2f"
	)
	t.Setenv("TOPH_ACCESS_CLIENT_ID", clientID)
	t.Setenv("TOPH_ACCESS_CLIENT_SECRET", clientSecret)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Id") != clientID || r.Header.Get("CF-Access-Client-Secret") != clientSecret {
			w.Header().Set("Location", "https://team.cloudflareaccess.com/cdn-cgi/access/login/host?kid=x&redirect_url=y")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		_, _ = io.WriteString(w, "origin")
	}))
	defer s.Close()

	without := httpCheck(s.URL)
	o := (Runner{}).Run(context.Background(), testkit.Config(), without)
	if o.Outcome != "fail" || o.Code != "unexpected_status" || o.Facts["edge_access"] != "login_redirect" {
		t.Fatalf("synthetic Access gate without tokens: %+v %v", o, o.Facts)
	}

	with := httpCheck(s.URL)
	with.HTTP.RequestHeaderEnvs = map[string]string{
		"CF-Access-Client-Id":     "TOPH_ACCESS_CLIENT_ID",
		"CF-Access-Client-Secret": "TOPH_ACCESS_CLIENT_SECRET",
	}
	o = (Runner{}).Run(context.Background(), testkit.Config(), with)
	if o.Outcome != "ok" || !o.Connectivity || o.Facts["peer_text"] != withheld || o.Facts["edge_access"] != "" {
		t.Fatalf("synthetic Access gate with tokens: %+v %v", o, o.Facts)
	}
	if o.Facts["edge_provider"] != "cloudflare" || o.Facts["http_status"] != "" {
		t.Fatalf("synthetic Access gate labels: %v", o.Facts)
	}
}

// A configured User-Agent wins over the probe's default, whether it is
// literal or read from the environment.
func TestHTTPConfiguredUserAgentWins(t *testing.T) {
	const agent = "topophany-test-agent/1.0"
	t.Setenv("TOPH_USER_AGENT", agent)
	var got atomic.Value
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("User-Agent"))
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	for _, tc := range []struct {
		name   string
		mutate func(*model.Check)
	}{
		{"literal", func(x *model.Check) {
			x.HTTP.RequestHeaders = map[string]string{"User-Agent": agent}
		}},
		{"environment", func(x *model.Check) {
			x.HTTP.RequestHeaderEnvs = map[string]string{"User-Agent": "TOPH_USER_AGENT"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := httpCheck(s.URL)
			tc.mutate(&x)
			if o := (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "ok" {
				t.Fatalf("configured User-Agent check: %+v", o)
			}
			if got.Load() != agent {
				t.Fatalf("server saw User-Agent %q", got.Load())
			}
		})
	}
}
