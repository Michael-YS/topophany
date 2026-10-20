package cloudflare

import "strings"

// maxTraceBytes bounds the body TraceFacts reads. A live trace body is a few
// hundred bytes; a larger one is treated as no evidence rather than scanned,
// because every field this parser keeps is short and a body over the cap is
// not a trace this probe can account for.
const maxTraceBytes = 4096

// Closed trace words. The parser compares them exactly; no case folding or
// trimming is part of the vocabulary.
const (
	traceWarpOff    = "off"
	traceWarpOn     = "on"
	traceWarpPlus   = "plus"
	traceGatewayOn  = "on"
	traceGatewayOff = "off"
	traceHTTP11     = "http1.1"
	traceHTTP2      = "http2"
	traceHTTP3      = "http3"
	traceTLS12      = "TLSv1.2"
	traceTLS13      = "TLSv1.3"
)

// TraceFacts reads the key=value lines of a /cdn-cgi/trace body, which any
// Cloudflare-proxied zone serves as the edge's own account of the request it
// answered. It is recognized only when both the fl and colo keys are present;
// a body over 4 KiB, or one missing either key, yields nil, false. The first
// occurrence of a repeated key is read, even when its value is then dropped,
// because the endpoint sends a fixed list and a later line cannot replace it.
// Lines without "=", lines with an empty key and every unknown key are
// ignored.
//
// Kept, each only in the form Cloudflare uses:
//   - trace_colo: colo, exactly three ASCII uppercase letters.
//   - trace_warp: warp, off, on or plus.
//   - trace_gateway: gateway, on or off.
//   - trace_http: http, mapping the slash form http/1.1, http/2 and http/3 to
//     http1.1, http2 and http3. The probe speaks HTTP/1.1, so a live check
//     reads http1.1.
//   - trace_tls: tls, TLSv1.2 or TLSv1.3.
//   - trace_loc: loc, the edge's view of the client country, exactly two
//     ASCII uppercase letters.
//
// The warp, gateway, http and tls fields are closed vocabularies; the colo
// and the country are free tokens checked only for their letter shape. The ip
// line is not a fact here: the probe reads it with its own exit parser as
// exit_ip. uag, h, sni, ts, fl, visit_scheme, sliver, kex and rbi are not
// kept: h and sni are hostnames the topology already knows, uag is our own
// agent string, and the rest is not evidence about the vantage point. No line
// or body text is ever returned, only the words above.
func TraceFacts(body []byte) (map[string]string, bool) {
	if len(body) > maxTraceBytes {
		return nil, false
	}
	out := map[string]string{}
	seen := map[string]bool{}
	hasFl, hasColo := false, false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSuffix(line, "\r")
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		switch key {
		case "fl":
			hasFl = true
		case "colo":
			hasColo = true
			if seen[key] {
				continue
			}
			seen[key] = true
			if upperLetters(value, 3) {
				// Detach the fact from the body: a substring would keep the
				// whole 4 KiB body alive for as long as the fact map lives.
				out["trace_colo"] = strings.Clone(value)
			}
		case "warp":
			if seen[key] {
				continue
			}
			seen[key] = true
			switch value {
			case traceWarpOff, traceWarpOn, traceWarpPlus:
				out["trace_warp"] = value
			}
		case "gateway":
			if seen[key] {
				continue
			}
			seen[key] = true
			switch value {
			case traceGatewayOn, traceGatewayOff:
				out["trace_gateway"] = value
			}
		case "http":
			if seen[key] {
				continue
			}
			seen[key] = true
			switch value {
			case "http/1.1":
				out["trace_http"] = traceHTTP11
			case "http/2":
				out["trace_http"] = traceHTTP2
			case "http/3":
				out["trace_http"] = traceHTTP3
			}
		case "tls":
			if seen[key] {
				continue
			}
			seen[key] = true
			switch value {
			case traceTLS12:
				out["trace_tls"] = traceTLS12
			case traceTLS13:
				out["trace_tls"] = traceTLS13
			}
		case "loc":
			if seen[key] {
				continue
			}
			seen[key] = true
			if upperLetters(value, 2) {
				out["trace_loc"] = strings.Clone(value)
			}
		}
	}
	if !hasFl || !hasColo {
		return nil, false
	}
	return out, true
}

// upperLetters reports whether s is exactly n ASCII uppercase letters, the
// shape a colo code and a country code have. Case and length are part of the
// shape, so lower-case and longer forms are not normalized.
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
