package probe

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

var edgeCompatibilityKeys = []string{
	"Cf-Ray", "Cf-Cache-Status", "X-Vercel-Id", "X-Vercel-Cache",
	"X-Amz-Cf-Pop", "X-Served-By", "X-Cache", "Server",
	"X-Nf-Request-Id", "Cache-Status",
}

func edgeCompatibilityHeaders() []http.Header {
	platforms := []http.Header{
		{"Cf-Ray": {"ray-NRT"}, "Cf-Cache-Status": {"dynamic"}},
		{"X-Vercel-Id": {"hnd1::iad1::request"}, "X-Vercel-Cache": {"miss"}},
		{"X-Amz-Cf-Pop": {"NRT57-P1"}, "X-Cache": {"Hit from cloudfront"}},
		{"X-Served-By": {"cache-iad-IAD, cache-nrt-NRT"}, "X-Cache": {"MISS, HIT"}},
		{"Server": {"Netlify"}, "Cache-Status": {`"Netlify Durable"; fwd=bypass, "Netlify Edge"; hit`}},
	}
	out := []http.Header{nil, {}, {"Server": {"nginx"}}, {"cf-ray": {"lowercase-map-key"}}, {"X-Nf-Request-Id": {"request"}}}
	// Freeze all combinations, including each pair and all five providers.
	for mask := 1; mask < 1<<len(platforms); mask++ {
		h := http.Header{}
		for bit, headers := range platforms {
			if mask&(1<<bit) != 0 {
				for key, values := range headers {
					h[key] = append(h[key], values...)
				}
			}
		}
		out = append(out, h)
	}
	values := []string{
		"", " ", "-", ",", "::", "a::b::c::d", "cache-", "CACHE-nrt-NRT",
		"HIT", "MISS, HIT", "LambdaGeneratedResponse from cloudfront",
		"FunctionGeneratedResponse from cloudfront", `"Netlify Edge"; hit; fwd=miss`,
		`"Netlify Edge"; fwd=miss; hit`, "private-token", "MiXeD",
		"x-NRT\x1b[2J\r\n", "x-东京-é-POP", "x-\xff\xfe", "x-" + strings.Repeat("A", 64),
	}
	for _, key := range edgeCompatibilityKeys {
		for _, value := range values {
			h := http.Header{}
			for _, platform := range platforms {
				// Select the relevant provider without changing unrelated signals.
				if _, exists := platform[key]; exists {
					h = platform.Clone()
					break
				}
			}
			h[key] = []string{value, "second-value-must-not-replace-first"}
			out = append(out, h)
		}
	}
	return out
}

func assertEdgeCompatibility(t *testing.T, headers http.Header) {
	t.Helper()
	before := headers.Clone()
	got, want := EdgeFacts(headers), edgeFactsBeforeSplit(headers)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headers %q: got %#v, pre-split %#v", headers, got, want)
	}
	if !reflect.DeepEqual(headers, before) {
		t.Fatal("header parsing mutated its input")
	}
	// Each observation must own its output even for an unrecognized provider.
	got["edge_provider"] = "caller-mutation"
	if !reflect.DeepEqual(EdgeFacts(headers), want) {
		t.Fatal("header parsing shares mutable output")
	}
}

func TestEdgeFactsCompatibilityBeforePackageSplit(t *testing.T) {
	for i, h := range edgeCompatibilityHeaders() {
		t.Run(fmt.Sprint(i), func(t *testing.T) { assertEdgeCompatibility(t, h) })
	}
}

func FuzzEdgeFactsCompatibility(f *testing.F) {
	for _, h := range edgeCompatibilityHeaders() {
		values := make([]string, len(edgeCompatibilityKeys))
		var mask uint16
		for i, key := range edgeCompatibilityKeys {
			values[i] = h.Get(key)
			if values[i] != "" {
				mask |= 1 << i
			}
		}
		f.Add([]byte(strings.Join(values, "\x00")), mask)
	}
	f.Fuzz(func(t *testing.T, raw []byte, mask uint16) {
		values := strings.SplitN(string(raw), "\x00", len(edgeCompatibilityKeys))
		h := http.Header{}
		for i, key := range edgeCompatibilityKeys {
			if mask&(1<<i) != 0 {
				h[key] = []string{values[i%len(values)]}
			}
		}
		assertEdgeCompatibility(t, h)
	})
}
