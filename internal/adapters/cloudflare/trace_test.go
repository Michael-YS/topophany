package cloudflare_test

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
)

// traceCapture reads the 2026-10-19 live body over HTTP/1.1. Its only changed
// line is the egress address, replaced by a documentation address. Only the
// colo, the WARP and Gateway states, the negotiated HTTP and TLS versions and
// the client country are kept; the hostnames, our own agent string and the
// remaining lines are not evidence about the vantage point.
func traceCapture(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile("testdata/2026-10-19/cdn-cgi-trace.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTraceFacts(t *testing.T) {
	liveTrace := traceCapture(t)
	for _, tc := range []struct {
		name string
		body string
		want map[string]string
		ok   bool
	}{
		{"live body", liveTrace, map[string]string{
			"trace_colo": "LAX", "trace_warp": "off", "trace_gateway": "off",
			"trace_http": "http1.1", "trace_tls": "TLSv1.3", "trace_loc": "US",
		}, true},
		{"minimal body", "fl=x\ncolo=HKG\n", map[string]string{"trace_colo": "HKG"}, true},
		{"missing fl", "colo=HKG\n", nil, false},
		{"missing colo", "fl=x\n", nil, false},
		{"fl without a value", "fl=\ncolo=HKG\n", map[string]string{"trace_colo": "HKG"}, true},
		{"empty values", "fl=\ncolo=\n", map[string]string{}, true},
		{"empty body", "", nil, false},
		{"fl is not a key", "fl\ncolo=HKG\n", nil, false},
		{"colo is not a key", "fl=x\ncolo\n", nil, false},
		{"empty key",
			"=x\nfl=x\ncolo=HKG\n", map[string]string{"trace_colo": "HKG"}, true},
		{"lines without equals",
			"fl=x\nnot a line\ncolo=HKG\nanother\n", map[string]string{"trace_colo": "HKG"}, true},
		{"unknown lines are dropped", "fl=x\ncolo=HKG\nuag=curl/8\nh=example.test\nip=203.0.113.8\nts=1\nvisit_scheme=https\nsni=plaintext\nsliver=050-tier1\nkex=X25519\nrbi=off\n",
			map[string]string{"trace_colo": "HKG"}, true},
		{"colo is three uppercase letters", "fl=x\ncolo=HKG\n", map[string]string{"trace_colo": "HKG"}, true},
		{"lowercase colo", "fl=x\ncolo=hkg\n", map[string]string{}, true},
		{"short colo", "fl=x\ncolo=HK\n", map[string]string{}, true},
		{"long colo", "fl=x\ncolo=HKGG\n", map[string]string{}, true},
		{"colo with a space", "fl=x\ncolo=H KG\n", map[string]string{}, true},
		{"colo with a control", "fl=x\ncolo=H\x1bGK\n", map[string]string{}, true},
		{"colo with non-ASCII letters", "fl=x\ncolo=H\xc4\xb0K\n", map[string]string{}, true},
		{"loc is two uppercase letters", "fl=x\ncolo=HKG\nloc=JP\n", map[string]string{"trace_colo": "HKG", "trace_loc": "JP"}, true},
		{"lowercase loc", "fl=x\ncolo=HKG\nloc=jp\n", map[string]string{"trace_colo": "HKG"}, true},
		{"three-letter loc", "fl=x\ncolo=HKG\nloc=JPN\n", map[string]string{"trace_colo": "HKG"}, true},
		{"warp off", "fl=x\ncolo=HKG\nwarp=off\n", map[string]string{"trace_colo": "HKG", "trace_warp": "off"}, true},
		{"warp on", "fl=x\ncolo=HKG\nwarp=on\n", map[string]string{"trace_colo": "HKG", "trace_warp": "on"}, true},
		{"warp plus", "fl=x\ncolo=HKG\nwarp=plus\n", map[string]string{"trace_colo": "HKG", "trace_warp": "plus"}, true},
		{"unknown warp", "fl=x\ncolo=HKG\nwarp=maybe\n", map[string]string{"trace_colo": "HKG"}, true},
		{"uppercase warp", "fl=x\ncolo=HKG\nwarp=ON\n", map[string]string{"trace_colo": "HKG"}, true},
		{"warp with a trailing space", "fl=x\ncolo=HKG\nwarp=on \n", map[string]string{"trace_colo": "HKG"}, true},
		{"gateway on", "fl=x\ncolo=HKG\ngateway=on\n", map[string]string{"trace_colo": "HKG", "trace_gateway": "on"}, true},
		{"gateway off", "fl=x\ncolo=HKG\ngateway=off\n", map[string]string{"trace_colo": "HKG", "trace_gateway": "off"}, true},
		{"gateway plus", "fl=x\ncolo=HKG\ngateway=plus\n", map[string]string{"trace_colo": "HKG"}, true},
		{"http slash form", "fl=x\ncolo=HKG\nhttp=http/1.1\n", map[string]string{"trace_colo": "HKG", "trace_http": "http1.1"}, true},
		{"http2", "fl=x\ncolo=HKG\nhttp=http/2\n", map[string]string{"trace_colo": "HKG", "trace_http": "http2"}, true},
		{"http3", "fl=x\ncolo=HKG\nhttp=http/3\n", map[string]string{"trace_colo": "HKG", "trace_http": "http3"}, true},
		{"http 1.0", "fl=x\ncolo=HKG\nhttp=http/1.0\n", map[string]string{"trace_colo": "HKG"}, true},
		{"http without the slash", "fl=x\ncolo=HKG\nhttp=http2\n", map[string]string{"trace_colo": "HKG"}, true},
		{"uppercase http", "fl=x\ncolo=HKG\nhttp=HTTP/2\n", map[string]string{"trace_colo": "HKG"}, true},
		{"tls 1.2", "fl=x\ncolo=HKG\ntls=TLSv1.2\n", map[string]string{"trace_colo": "HKG", "trace_tls": "TLSv1.2"}, true},
		{"tls 1.3", "fl=x\ncolo=HKG\ntls=TLSv1.3\n", map[string]string{"trace_colo": "HKG", "trace_tls": "TLSv1.3"}, true},
		{"tls 1.1", "fl=x\ncolo=HKG\ntls=TLSv1.1\n", map[string]string{"trace_colo": "HKG"}, true},
		{"lowercase tls", "fl=x\ncolo=HKG\ntls=tlsv1.3\n", map[string]string{"trace_colo": "HKG"}, true},
		{"repeated keys keep the first", "fl=x\ncolo=HKG\ncolo=NRT\nwarp=on\nwarp=off\n",
			map[string]string{"trace_colo": "HKG", "trace_warp": "on"}, true},
		{"the first repeated value is still the one dropped", "fl=x\ncolo=hkg\ncolo=NRT\n",
			map[string]string{}, true},
		{"CRLF lines", "fl=x\r\ncolo=HKG\r\nwarp=on\r\nhttp=http/2\r\n",
			map[string]string{"trace_colo": "HKG", "trace_warp": "on", "trace_http": "http2"}, true},
		{"CRLF with a bare carriage return", "fl=x\rcolo=HKG\r", nil, false},
		{"control characters around the keys", "fl=x\x00\ncolo\x1b=HKG\ncolo=HKG\n",
			map[string]string{"trace_colo": "HKG"}, true},
		{"control character in a value", "fl=x\ncolo=HKG\nwarp=on\x00\ntls=TLSv1.3\x1b\n",
			map[string]string{"trace_colo": "HKG"}, true},
		{"the ip line is not a fact", "fl=x\ncolo=HKG\nip=203.0.113.8\n",
			map[string]string{"trace_colo": "HKG"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cloudflare.TraceFacts([]byte(tc.body))
			if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, %v; want %#v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// A body of exactly 4 KiB is inside the bound; one byte more is not parsed,
// even when the lines that would recognize it come first.
func TestTraceByteLimit(t *testing.T) {
	const prefix = "fl=x\ncolo=HKG\n"
	atLimit := prefix + strings.Repeat("x\n", (4096-len(prefix))/2)
	if len(atLimit) != 4096 {
		t.Fatal(len(atLimit))
	}
	if got, ok := cloudflare.TraceFacts([]byte(atLimit)); !ok || got["trace_colo"] != "HKG" {
		t.Fatalf("a body at the limit was not parsed: %#v, %v", got, ok)
	}
	if got, ok := cloudflare.TraceFacts([]byte(atLimit + "x")); ok || got != nil {
		t.Fatalf("a body over the limit was parsed: %#v, %v", got, ok)
	}
	if got, ok := cloudflare.TraceFacts([]byte(prefix + strings.Repeat("x", 1<<20))); ok || got != nil {
		t.Fatalf("a one MiB body was parsed: %#v, %v", got, ok)
	}
}

// FuzzTraceFacts keeps the parser total on arbitrary bodies and pins what it
// promises: a body over 4 KiB yields nothing, recognition requires an fl= and
// a colo= line, the first occurrence of a repeated kept key wins, every kept
// value is in its closed vocabulary or letter shape, and the body is never
// modified.
func FuzzTraceFacts(f *testing.F) {
	f.Add(traceCapture(f))
	f.Add("fl=x\ncolo=HKG\nwarp=on\ngateway=off\nhttp=http/2\ntls=TLSv1.2\nloc=JP\n")
	f.Add("fl=\ncolo=\n")
	f.Add("colo=HKG\n")
	f.Add("fl=x\ncolo=hkg\ncolo=NRT\n")
	f.Add("fl=x\r\ncolo=LAX\r\nwarp=on\x00\r\n")
	f.Add("fl=x\ncolo=H\x1bGK\nwarp=on\n")
	f.Add("fl=x\ncolo=HKG\nhttp=http/1.1=extra\n")
	f.Add(strings.Repeat("fl=x\ncolo=HKG\n", 500))
	f.Add(strings.Repeat("x", 1<<20))
	f.Fuzz(func(t *testing.T, body string) {
		raw := []byte(body)
		before := bytes.Clone(raw)
		got, ok := cloudflare.TraceFacts(raw)
		if !bytes.Equal(raw, before) {
			t.Fatal("parsing mutated its input")
		}
		if len(raw) > 4096 {
			if ok || len(got) != 0 {
				t.Fatalf("a %d-byte body produced %#v, %v", len(raw), got, ok)
			}
			return
		}
		if !ok {
			if len(got) != 0 {
				t.Fatalf("an unrecognized body produced %#v", got)
			}
			return
		}
		if !traceLine(body, "fl") || !traceLine(body, "colo") {
			t.Fatal("recognized a body without fl= and colo= lines")
		}
		repeated := body
		if !strings.HasSuffix(repeated, "\n") {
			repeated += "\n"
		}
		repeated += "colo=ZZZ\n"
		if traceLine(body, "warp") {
			repeated += "warp=plus\n"
		}
		if len(repeated) <= 4096 {
			again, againOK := cloudflare.TraceFacts([]byte(repeated))
			if !againOK {
				t.Fatalf("a recognized body was not recognized with duplicate lines: %q", body)
			}
			if again["trace_colo"] != got["trace_colo"] {
				t.Fatalf("a later colo replaced the first: %#v then %#v", got, again)
			}
			if traceLine(body, "warp") && again["trace_warp"] != got["trace_warp"] {
				t.Fatalf("a later warp replaced the first: %#v then %#v", got, again)
			}
		}
		for k, v := range got {
			switch k {
			case "trace_colo":
				if !upperLetters(v, 3) {
					t.Fatalf("colo %q is outside its shape", v)
				}
			case "trace_loc":
				if !upperLetters(v, 2) {
					t.Fatalf("country %q is outside its shape", v)
				}
			case "trace_warp":
				if v != "off" && v != "on" && v != "plus" {
					t.Fatalf("warp %q is outside the closed vocabulary", v)
				}
			case "trace_gateway":
				if v != "on" && v != "off" {
					t.Fatalf("gateway %q is outside the closed vocabulary", v)
				}
			case "trace_http":
				if v != "http1.1" && v != "http2" && v != "http3" {
					t.Fatalf("http %q is outside the closed vocabulary", v)
				}
			case "trace_tls":
				if v != "TLSv1.2" && v != "TLSv1.3" {
					t.Fatalf("tls %q is outside the closed vocabulary", v)
				}
			default:
				t.Fatalf("unexpected fact %q", k)
			}
		}
	})
}

// traceLine says whether body carries a line with exactly this key, the
// recognition rule the fuzz target checks independently of the parser.
func traceLine(body, key string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if k, _, ok := strings.Cut(line, "="); ok && k == key {
			return true
		}
	}
	return false
}

func upperLetters(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < n; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}
