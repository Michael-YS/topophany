package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Cloudflare's cfL4 Server-Timing entry is the colo's own reading of the same
// connection (TCP or QUIC) the worker measured. Its RTTs and counts are
// numbers and its protocol is a closed word, so all five survive a secret
// request, while the POP and HTTP status do not; no text of the entry reaches
// the observation.
func TestHTTPRecordsCloudflareEdgeRTT(t *testing.T) {
	const (
		token  = "nrt1sin1-a1b2c3d4e5f6a7b8c9d0e1f2"
		timing = `cfL4;desc="?proto=TCP&rtt=1620&min_rtt=1536&rtt_var=452&sent=5&recv=8&lost=2&retrans=3&sent_bytes=2837&recv_bytes=496&delivery_rate=2247915&cwnd=53&unsent_bytes=0&cid=` + token + `&ts=226&x=0"`
	)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		w.Header().Set("Cf-Cache-Status", "HIT")
		w.Header().Set("Server-Timing", timing)
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	want := map[string]string{
		"edge_rtt_ms":     "2",
		"edge_min_rtt_ms": "2",
		"edge_retrans":    "3",
		"edge_lost":       "2",
		"edge_proto":      "TCP",
	}

	public := (Runner{}).Run(context.Background(), testkit.Config(), httpCheck(s.URL))
	if public.Outcome != "ok" || public.Facts["edge_provider"] != "cloudflare" || public.Facts["edge_pop"] != "NRT" || public.Facts["http_status"] != "200" {
		t.Fatalf("public request: %+v", public)
	}
	for k, v := range want {
		if public.Facts[k] != v {
			t.Fatalf("public fact %q = %q; want %q (%#v)", k, public.Facts[k], v, public.Facts)
		}
	}
	if public.Facts["peer_text"] != "" {
		t.Fatal("a public request was withheld", public.Facts)
	}

	secretCheck := httpCheck(s.URL)
	secretCheck.HTTP.RequestHeaders = map[string]string{"X-Key": token}
	secret := (Runner{}).Run(context.Background(), testkit.Config(), secretCheck)
	if secret.Outcome != "ok" || secret.Facts["edge_provider"] != "cloudflare" || secret.Facts["peer_text"] != withheld {
		t.Fatalf("secret request: %+v", secret)
	}
	for k, v := range want {
		if secret.Facts[k] != v {
			t.Fatalf("secret fact %q = %q; want %q (%#v)", k, secret.Facts[k], v, secret.Facts)
		}
	}
	if secret.Facts["edge_pop"] != "" || secret.Facts["http_status"] != "" {
		t.Fatal("free labels survived a secret request", secret.Facts)
	}
	for name, o := range map[string]model.Observation{"public": public, "secret": secret} {
		if code := config.ObservationBounds(o, config.MaxTook); code != "" {
			t.Fatalf("%s observation refused: %s", name, code)
		}
		for _, k := range []string{"edge_rtt_ms", "edge_min_rtt_ms", "edge_retrans", "edge_lost", "edge_proto"} {
			if _, ok := o.Timings[k]; ok {
				t.Fatalf("%s carried cfL4 fact %q as a timing; they are facts", name, k)
			}
		}
	}
	b, err := json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"cfL4", "desc=", "rtt_var", "sent=", "recv=", "sent_bytes", "recv_bytes",
		"delivery_rate", "cwnd", "unsent_bytes", "ts=226", "x=0",
		token[:12], token[12:],
	} {
		if strings.Contains(string(b), part) {
			t.Fatalf("observation carries %q: %s", part, b)
		}
	}
}

// A purely numeric credential can be handed back as a bounded number, and the
// secret boundary admits numbers as a class: the counts are kept verbatim
// rather than withheld. This pins that documented numeric exception; it is
// not a leak of a free label, and the same value would appear rounded in an
// RTT reading.
func TestHTTPSecretNumericCountsSurvive(t *testing.T) {
	const numericSecret = "12345678"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		w.Header().Set("Server-Timing", `cfL4;desc="?lost=12345678&retrans=12345678"`)
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	x := httpCheck(s.URL)
	x.HTTP.RequestHeaders = map[string]string{"X-Key": numericSecret}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["edge_provider"] != "cloudflare" || o.Facts["peer_text"] != withheld || o.Facts["edge_pop"] != "" || o.Facts["http_status"] != "" {
		t.Fatalf("numeric secret request: %+v", o)
	}
	if o.Facts["edge_lost"] != numericSecret || o.Facts["edge_retrans"] != numericSecret {
		t.Fatalf("bounded counts dropped the numeric value: %#v", o.Facts)
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"cfL4", "desc=", "lost=", "retrans=", "cid"} {
		if strings.Contains(string(b), part) {
			t.Fatalf("observation carries header text %q: %s", part, b)
		}
	}
	// The numeric credential is allowed to appear only as the two bounded
	// counts, never as free text in another fact.
	for k, v := range o.Facts {
		if strings.Contains(v, numericSecret) && k != "edge_lost" && k != "edge_retrans" {
			t.Fatalf("numeric secret reached fact %q: %q", k, v)
		}
	}
}

// rtt and min_rtt are distinct fields: the keys must not be swapped.
func TestHTTPDistinguishesEdgeRTTFromMinRTT(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		w.Header().Set("Server-Timing", `cfL4;desc="?rtt=2500&min_rtt=1200"`)
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	o := (Runner{}).Run(context.Background(), testkit.Config(), httpCheck(s.URL))
	if o.Facts["edge_rtt_ms"] != "3" || o.Facts["edge_min_rtt_ms"] != "1" {
		t.Fatalf("rtt and min_rtt were not distinguished: %#v", o.Facts)
	}
}

// The entry is read only after Cloudflare named itself: a Vercel response, or
// one with no provider header, keeps nothing from a cfL4 entry, and a
// Cloudflare response without the entry gains nothing.
func TestHTTPDoesNotReadServerTimingWithoutCloudflare(t *testing.T) {
	const timing = `cfL4;desc="?proto=TCP&rtt=1000&lost=1"`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vercel":
			w.Header().Set("X-Vercel-Id", "hnd1::iad1::request")
			w.Header().Set("Server-Timing", timing)
		case "/no-provider":
			w.Header().Set("Server-Timing", timing)
		case "/no-timing":
			w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()
	for _, tc := range []struct {
		path, provider string
	}{
		{"/vercel", "vercel"},
		{"/no-provider", ""},
		{"/no-timing", "cloudflare"},
	} {
		o := (Runner{}).Run(context.Background(), testkit.Config(), httpCheck(s.URL+tc.path))
		if o.Outcome != "ok" || o.Facts["edge_provider"] != tc.provider {
			t.Fatalf("%s: %+v", tc.path, o)
		}
		for _, k := range []string{"edge_rtt_ms", "edge_min_rtt_ms", "edge_retrans", "edge_lost", "edge_proto"} {
			if v, ok := o.Facts[k]; ok {
				t.Fatalf("%s read a cfL4 fact without Cloudflare: %s=%q", tc.path, k, v)
			}
		}
	}
}
