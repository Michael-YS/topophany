package cloudflare_test

import (
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
)

func serverTiming(values ...string) http.Header {
	h := http.Header{}
	for _, v := range values {
		h.Add("Server-Timing", v)
	}
	return h
}

// Cloudflare's cfL4 entry is one of several Server-Timing metrics and its
// desc is a query string, quoted or not. Only the connection's RTT readings,
// counts and protocol word are facts; the identifiers and byte counters in
// the same entry are not.
func TestServerTimingFacts(t *testing.T) {
	const full = `cfL4;desc="?proto=TCP&rtt=1014&min_rtt=1012&rtt_var=290&sent=5&recv=7&lost=0&retrans=0&sent_bytes=2839&recv_bytes=496&delivery_rate=3933852&cwnd=53&unsent_bytes=0&cid=0123456789abcdef&ts=204&x=0"`
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    map[string]string
	}{
		{"no header", serverTiming(), nil},
		{"full entry", serverTiming(full), map[string]string{"edge_rtt_ms": "1", "edge_min_rtt_ms": "1", "edge_retrans": "0", "edge_lost": "0", "edge_proto": "TCP"}},
		{"unquoted desc", serverTiming(`cfL4;desc=?proto=TCP&rtt=1620&min_rtt=1536&lost=3&retrans=4`), map[string]string{"edge_rtt_ms": "2", "edge_min_rtt_ms": "2", "edge_retrans": "4", "edge_lost": "3", "edge_proto": "TCP"}},
		{"no leading question mark", serverTiming(`cfL4;desc="proto=QUIC&rtt=38"`), map[string]string{"edge_rtt_ms": "0", "edge_proto": "QUIC"}},
		{"other entries around it", serverTiming(`cache;desc="HIT", cfL4;desc="?rtt=1000", edge;dur=2`), map[string]string{"edge_rtt_ms": "1"}},
		{"quoted comma", serverTiming(`cfL4;desc="?proto=TCP&x=a,b&rtt=2000"`), map[string]string{"edge_rtt_ms": "2", "edge_proto": "TCP"}},
		{"quoted semicolon", serverTiming(`cfL4;desc="?rtt=1000&x=a;b&lost=2"`), map[string]string{"edge_rtt_ms": "1", "edge_lost": "2"}},
		{"escaped quote", serverTiming(`cfL4;desc="?x=a\"b&rtt=3000"`), map[string]string{"edge_rtt_ms": "3"}},
		{"escaped backslash", serverTiming(`cfL4;desc="?x=a\\b&rtt=3000"`), map[string]string{"edge_rtt_ms": "3"}},
		{"desc after another parameter", serverTiming(`cfL4;dur=2;desc="?rtt=1000&min_rtt=500"`), map[string]string{"edge_rtt_ms": "1", "edge_min_rtt_ms": "1"}},
		{"rtt and min_rtt are distinct", serverTiming(`cfL4;desc="?rtt=2500&min_rtt=1200"`), map[string]string{"edge_rtt_ms": "3", "edge_min_rtt_ms": "1"}},
		{"only unknown fields", serverTiming(`cfL4;desc="?sent=5&recv=7&cid=0123456789abcdef&ts=204&x=0&sent_bytes=1&recv_bytes=2&cwnd=3&delivery_rate=4&rtt_var=5"`), nil},
		{"other metrics only", serverTiming(`cfWorker;dur=7, cfL4x;desc="?rtt=1000", cache;desc=DYNAMIC`), nil},
		{"entry name is exact", serverTiming(`CFL4;desc="?rtt=1000"`), nil},
		{"missing desc", serverTiming(`cfL4;dur=2`), nil},
		{"empty quoted desc", serverTiming(`cfL4;desc=""`), nil},
		{"empty bare desc", serverTiming(`cfL4;desc=`), nil},
		{"unterminated quote", serverTiming(`cfL4;desc="?rtt=1000`), nil},
		{"text after the closing quote", serverTiming(`cfL4;desc="?rtt=1000"x`), nil},
		{"first cfL4 wins", serverTiming(`cfL4;desc="?rtt=1000", cfL4;desc="?rtt=2000"`), map[string]string{"edge_rtt_ms": "1"}},
		{"first cfL4 without desc wins", serverTiming(`cfL4;dur=2, cfL4;desc="?rtt=1000"`), nil},
		{"first desc parameter wins", serverTiming(`cfL4;desc="?rtt=1000";desc="?rtt=2000"`), map[string]string{"edge_rtt_ms": "1"}},
		{"repeated query keys keep the first", serverTiming(`cfL4;desc="?rtt=1000&rtt=2000&lost=7&lost=9"`), map[string]string{"edge_rtt_ms": "1", "edge_lost": "7"}},
		{"malformed pair keeps its neighbours", serverTiming(`cfL4;desc="?rtt=1000&bad;x=1&lost=2"`), map[string]string{"edge_rtt_ms": "1", "edge_lost": "2"}},
		{"bad escape keeps its neighbours", serverTiming(`cfL4;desc="?rtt=%zz&lost=2"`), map[string]string{"edge_lost": "2"}},
		{"malformed min_rtt", serverTiming(`cfL4;desc="?rtt=1000&min_rtt=abc&lost=2"`), map[string]string{"edge_rtt_ms": "1", "edge_lost": "2"}},
		{"malformed retrans", serverTiming(`cfL4;desc="?rtt=1000&retrans=-1&lost=2"`), map[string]string{"edge_rtt_ms": "1", "edge_lost": "2"}},
		{"malformed lost", serverTiming(`cfL4;desc="?rtt=1000&lost=1.5&retrans=2"`), map[string]string{"edge_rtt_ms": "1", "edge_retrans": "2"}},
		{"protocol vocabulary", serverTiming(`cfL4;desc="?proto=QuIc&rtt=1"`), map[string]string{"edge_rtt_ms": "0", "edge_proto": "QUIC"}},
		{"unknown protocol dropped", serverTiming(`cfL4;desc="?proto=UDP&rtt=1000&lost=1"`), map[string]string{"edge_rtt_ms": "1", "edge_lost": "1"}},
		{"control characters in a field", serverTiming("cfL4;desc=\"?rtt=10\x1b00&lost=2\""), map[string]string{"edge_lost": "2"}},
		{"control characters in the entry name", serverTiming("cfL4\x1b;desc=\"?rtt=1000\""), nil},
		{"multiple header lines", serverTiming(`cache;desc="HIT"`, `cfL4;desc="?rtt=1000&proto=TCP"`), map[string]string{"edge_rtt_ms": "1", "edge_proto": "TCP"}},
		{"first header line's cfL4 wins", serverTiming(`cfL4;desc="?rtt=1000"`, `cfL4;desc="?rtt=2000"`), map[string]string{"edge_rtt_ms": "1"}},
		{"padded but under the limit", serverTiming(`cfL4;desc="?rtt=1000&x=` + strings.Repeat("a", 500) + `"`), map[string]string{"edge_rtt_ms": "1"}},
		{"over the combined limit", serverTiming(full + strings.Repeat(" ", 2048)), nil},
		{"two values over the combined limit", serverTiming(`cfL4;desc="?rtt=1000&x=`+strings.Repeat("a", 1500)+`"`, `cfL4;desc="?rtt=2000&x=`+strings.Repeat("b", 1500)+`"`), nil},
		{"one MiB", serverTiming(`cfL4;desc="?rtt=1000&x=` + strings.Repeat("a", 1<<20) + `"`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cloudflare.ServerTimingFacts(tc.headers)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v; want %#v", got, tc.want)
			}
		})
	}
}

// RTTs are microseconds in the live capture and become whole milliseconds,
// rounded to the nearest, halves up. Integers are one to twelve ASCII digits:
// every other spelling a peer could send is not a number this evidence may
// carry.
func TestServerTimingIntegersAndRounding(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{"0", "0"},
		{"499", "0"},
		{"500", "1"},
		{"1014", "1"},
		{"1499", "1"},
		{"1500", "2"},
		{"1620", "2"},
		{"0001620", "2"},
		{"999999999999", "1000000000"},
		{"1234567890123", ""},
		{"+1000", ""},
		{"-1000", ""},
		{" 1000", ""},
		{"1000 ", ""},
		{"10.5", ""},
		{"1e3", ""},
		{"0x10", ""},
		{"", ""},
		{"١٠٠٠", ""},
		{"１０００", ""},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got := cloudflare.ServerTimingFacts(serverTiming(`cfL4;desc="?rtt=` + tc.value + `&lost=2"`))
			if tc.want == "" {
				if len(got) != 1 || got["edge_lost"] != "2" {
					t.Fatalf("value %q produced %#v; a malformed rtt must not discard lost", tc.value, got)
				}
				return
			}
			if got["edge_rtt_ms"] != tc.want || len(got) != 2 {
				t.Fatalf("value %q produced %#v; want edge_rtt_ms %q", tc.value, got, tc.want)
			}
		})
	}
}

// A value of exactly 2 KiB is inside the bound; one byte more is not.
func TestServerTimingByteLimit(t *testing.T) {
	makeValue := func(n int) string {
		const prefix = `cfL4;desc="?rtt=1000&x=`
		const suffix = `"`
		return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
	}
	atLimit := makeValue(2048)
	if len(atLimit) != 2048 {
		t.Fatal(len(atLimit))
	}
	if got := cloudflare.ServerTimingFacts(serverTiming(atLimit)); got["edge_rtt_ms"] != "1" {
		t.Fatalf("a value at the limit was not parsed: %#v", got)
	}
	if got := cloudflare.ServerTimingFacts(serverTiming(makeValue(2049))); got != nil {
		t.Fatalf("a value over the limit was parsed: %#v", got)
	}
	// The cap is on the sum: two values of exactly 2 KiB together are inside.
	first := `cfL4;desc="?rtt=1000"`
	second := strings.Repeat("a", 2048-len(first))
	if got := cloudflare.ServerTimingFacts(serverTiming(first, second)); got["edge_rtt_ms"] != "1" {
		t.Fatalf("two values of exactly 2 KiB together were not parsed: %#v", got)
	}
	if got := cloudflare.ServerTimingFacts(serverTiming(first, second+"a")); got != nil {
		t.Fatalf("two values over the limit were parsed: %#v", got)
	}
}

// The capture from testdata/2026-10-19 is the shape the parser was written
// against; its only sanitized field is cid. The exact map is the whole
// output, so no header text can be present beyond it.
func TestServerTimingCapture(t *testing.T) {
	b, err := os.ReadFile("testdata/2026-10-19/server-timing.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "cid=0000000000000000") {
		t.Fatal("the fixture does not carry the redacted connection identifier")
	}
	got := cloudflare.ServerTimingFacts(serverTiming(strings.TrimSpace(string(b))))
	want := map[string]string{"edge_rtt_ms": "1", "edge_min_rtt_ms": "1", "edge_retrans": "0", "edge_lost": "0", "edge_proto": "TCP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

// FuzzServerTimingFacts keeps the parser total on arbitrary header values and
// pins every output to the small shape it documents: digits, or one of two
// closed protocol words. A header over the combined byte limit yields nothing.
// NUL separates header lines, since a real header value cannot contain one.
func FuzzServerTimingFacts(f *testing.F) {
	f.Add(`cfL4;desc="?proto=TCP&rtt=1014&min_rtt=1012&lost=0&retrans=0&cid=0123456789abcdef&ts=204&x=0"`)
	f.Add(`cache;desc="HIT", cfL4;desc="?rtt=38&proto=QUIC"`)
	f.Add("cfL4;desc=?rtt=1620&lost=3\x00cfWorker;dur=7")
	f.Add(`cfL4;desc="?rtt=999999999999999999999&lost=2"`)
	f.Add("cfL4;desc=\"?rtt=1\x1b[2J&lost=2\"")
	f.Add(`cfL4;desc="?rtt=1000`)
	f.Add(`CFL4;desc="?rtt=1000", cfL4x;desc="?rtt=2000"`)
	f.Add(strings.Repeat("a", 1<<20))
	f.Add(strings.Repeat(`cfL4;desc="?rtt=1&x=`, 200))
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) {
		values := strings.Split(raw, "\x00")
		h := http.Header{}
		total := 0
		for _, v := range values {
			total += len(v)
			h.Add("Server-Timing", v)
		}
		before := h.Clone()
		got := cloudflare.ServerTimingFacts(h)
		if !reflect.DeepEqual(h, before) {
			t.Fatal("parsing mutated its input")
		}
		if total > 2048 && len(got) != 0 {
			t.Fatalf("a %d-byte header produced %#v", total, got)
		}
		for k, v := range got {
			switch k {
			case "edge_rtt_ms", "edge_min_rtt_ms", "edge_retrans", "edge_lost":
				if !decimal(v) {
					t.Fatalf("fact %q is not a canonical decimal: %q", k, v)
				}
			case "edge_proto":
				if v != "TCP" && v != "QUIC" {
					t.Fatalf("protocol %q is outside the closed vocabulary", v)
				}
			default:
				t.Fatalf("unexpected fact %q", k)
			}
		}
	})
}

// decimal reports whether s is a canonical decimal integer: digits only, no
// leading zero unless the value is zero, at most twelve digits.
func decimal(s string) bool {
	if len(s) == 0 || len(s) > 12 || len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
