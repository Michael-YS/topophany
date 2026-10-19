package cloudflare

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// maxServerTimingBytes bounds the bytes of Server-Timing the parser reads: the
// sum of all values. A live cfL4 entry is about 250 bytes; a larger set is
// treated as no evidence rather than scanned, because the entry is optional
// and the probe gets nothing here it needs beyond a few counters. The cap is
// on the bytes, not on the number of entries, so an intermediary that splits
// the entry across lines cannot enlarge the work.
const maxServerTimingBytes = 2048

// ServerTimingFacts reads Cloudflare's cfL4 entry from Server-Timing: the
// colo's own counters for the connection (TCP or QUIC) that carried the
// response. Cloudflare adds it to some responses only, and this function is
// therefore optional evidence: no entry or no usable field yields nil. The
// combined length of all Server-Timing values is capped at 2 KiB; a larger
// header is not parsed.
//
// The entry is a Server-Timing metric named cfL4 whose desc parameter holds
// a query string, quoted or unquoted, such as
//
//	cfL4;desc="?proto=TCP&rtt=1620&min_rtt=1536&retrans=0&lost=0&cid=…"
//
// The first entry named cfL4 is read, exactly as Cloudflare spells it; other
// metrics, including cfWorker and a lookalike such as cfL4x, are ignored. A
// later cfL4 entry is not consulted when the first has no usable desc. Only
// the first value of a repeated desc parameter or repeated query key is
// read. The query is parsed with url.ParseQuery after a leading "?" is
// trimmed; it skips a malformed pair (a bare semicolon, a bad escape) and
// keeps the valid pairs on either side, and a field that fails its own rule
// never discards the others.
//
// Kept:
//   - edge_rtt_ms, edge_min_rtt_ms: rtt and min_rtt, which the live capture
//     shows in microseconds (values around one to two thousand cannot be
//     milliseconds or nanoseconds), rounded to the nearest whole millisecond,
//     halves up, so 1620 µs reads 2 and 38 µs reads 0.
//   - edge_retrans, edge_lost: retrans and lost, decimal counts.
//   - edge_proto: proto, only the closed words TCP and QUIC, normalized to
//     upper case. Any other word is dropped.
//
// Every integer is one to twelve ASCII digits and nothing else: signs,
// spaces, decimals, exponents, empty values and longer runs are refused. The
// connection identifier (cid) and the byte, window, rate and timestamp
// fields (sent_bytes, recv_bytes, cwnd, delivery_rate, x, ts) are never kept,
// nor is anything else in the entry. No part of the header is ever returned
// as it arrived: facts are numbers, one closed word or nothing.
func ServerTimingFacts(h http.Header) map[string]string {
	values := h.Values("Server-Timing")
	total := 0
	for _, v := range values {
		total += len(v)
		if total > maxServerTimingBytes {
			return nil
		}
	}
	for _, value := range values {
		for _, entry := range splitOutsideQuotes(value, ',') {
			parts := splitOutsideQuotes(entry, ';')
			if strings.TrimSpace(parts[0]) != "cfL4" {
				continue
			}
			desc, ok := descParam(parts[1:])
			if !ok {
				return nil
			}
			return parseCFL4Query(desc)
		}
	}
	return nil
}

// splitOutsideQuotes splits s at sep bytes that are not inside a quoted
// string, the way Server-Timing separates entries and parameters. A
// backslash inside a quoted string escapes the next byte, as in an HTTP
// quoted-string, so a quoted desc may hold a comma or a semicolon.
func splitOutsideQuotes(s string, sep byte) []string {
	out := []string{}
	start := 0
	quoted, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quoted {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case sep:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// descParam returns the first desc parameter's value from a cfL4 entry's
// parameter list, unquoted. A missing or malformed desc yields false; an
// empty quoted desc returns an empty value, whose query then yields no facts
// in parseCFL4Query.
func descParam(params []string) (string, bool) {
	for _, p := range params {
		name, raw, ok := strings.Cut(p, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "desc") {
			continue
		}
		return unquoteServerTiming(raw)
	}
	return "", false
}

// unquoteServerTiming reads a Server-Timing parameter value: a quoted string,
// whose backslash escapes are removed, or a bare token. A quoted value must
// close before anything but spaces, so trailing text cannot extend a desc.
func unquoteServerTiming(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if s[0] != '"' {
		if strings.ContainsAny(s, "\"") {
			return "", false
		}
		return s, true
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
			if i == len(s) {
				return "", false
			}
			b.WriteByte(s[i])
		case '"':
			if strings.TrimSpace(s[i+1:]) != "" {
				return "", false
			}
			return b.String(), true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", false
}

func parseCFL4Query(desc string) map[string]string {
	desc = strings.TrimSpace(desc)
	desc = strings.TrimPrefix(desc, "?")
	// url.ParseQuery skips a malformed pair (a bare semicolon or a bad
	// escape, say) and keeps the valid pairs on either side, which is exactly
	// what a malformed field should do: its neighbours are still evidence.
	q, _ := url.ParseQuery(desc)
	out := map[string]string{}
	if v, ok := firstQueryValue(q, "rtt"); ok {
		if n, ok := strictCount(v); ok {
			out["edge_rtt_ms"] = strconv.FormatInt((n+500)/1000, 10)
		}
	}
	if v, ok := firstQueryValue(q, "min_rtt"); ok {
		if n, ok := strictCount(v); ok {
			out["edge_min_rtt_ms"] = strconv.FormatInt((n+500)/1000, 10)
		}
	}
	if v, ok := firstQueryValue(q, "retrans"); ok {
		if n, ok := strictCount(v); ok {
			out["edge_retrans"] = strconv.FormatInt(n, 10)
		}
	}
	if v, ok := firstQueryValue(q, "lost"); ok {
		if n, ok := strictCount(v); ok {
			out["edge_lost"] = strconv.FormatInt(n, 10)
		}
	}
	if v, ok := firstQueryValue(q, "proto"); ok {
		switch p := strings.ToUpper(strings.TrimSpace(v)); p {
		case "TCP", "QUIC":
			out["edge_proto"] = p
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstQueryValue(q url.Values, key string) (string, bool) {
	v, ok := q[key]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

// strictCount reads a Server-Timing integer: one to twelve ASCII digits and
// nothing else. A sign, a space, a decimal point, an exponent, an empty
// value or a thirteenth digit is not a number this evidence can carry. The
// result fits an int64 on every build, wasm included.
func strictCount(s string) (int64, bool) {
	if len(s) == 0 || len(s) > 12 {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int64(s[i]-'0')
	}
	return n, true
}
