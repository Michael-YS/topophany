package cloudflared

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
)

// MetricsBudget is the most a /metrics body may be. The live capture was
// 16 KiB; 1 MiB leaves room for the exporter's unrelated Go and process
// families without letting a foreign listener fill memory.
const MetricsBudget = 1 << 20

// Bounds for one exposition line. A line over these is skipped whole, so a
// single hostile sample cannot allocate without limit.
const (
	maxMetricLabels        = 32
	maxMetricLabelName     = 64
	maxMetricLabelValue    = 256
	maxMetricIntegerDigits = 18
	maxConnectorVersion    = 24
)

// A version is a bounded token: the observed connector version 2026.10.0 is
// nine characters, and a bound that could not hold it would hide the very
// value the fact exists to report. Prerelease suffixes stay inside 24.
var versionToken = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,23}$`)

// allowedFamilies are the only metric families this adapter reads. Every
// other line, including the exporter's Go and process families and
// cloudflared_tunnel_response_by_code, is skipped once its name is read.
var allowedFamilies = map[string]bool{
	"cloudflared_tunnel_server_locations": true,
	"cloudflared_tunnel_ha_connections":   true,
	"cloudflared_tunnel_total_requests":   true,
	"cloudflared_tunnel_request_errors":   true,
	"cloudflared_tunnel_active_streams":   true,
	"cloudflared_tunnel_timer_retries":    true,
	"build_info":                          true,
}

// Metrics is what the allowlisted families reported. A nil count means the
// family was absent, which is not the same as a reported zero. TimerRetries
// is parsed because the allowlist names it, but no observation fact carries
// it: the eight-fact budget is spent on the readings the roadmap defines.
type Metrics struct {
	ServerLocations  []string // lowercase colos, sorted, at most eight
	HAConnections    *int64
	TotalRequests    *int64
	RequestErrors    *int64
	ActiveStreams    *int64
	TimerRetries     *int64
	ConnectorVersion string
	parsed           bool
}

// Parsed reports whether at least one allowlisted sample carried a readable
// integer. A listener that answers 200 with no known family is not readable
// telemetry, and the adapter treats it as a monitor error.
func (m Metrics) Parsed() bool { return m.parsed }

// Count returns a count as a decimal fact and whether it was reported.
func (m Metrics) Count(p *int64) (string, bool) {
	if p == nil {
		return "", false
	}
	return decimal(*p), true
}

// ParseMetrics reads the allowlisted families from a Prometheus text
// exposition. It never returns line text: labels are used by name, and every
// other sample is skipped. Malformed lines and malformed values are skipped,
// not errors, because the body is only 1 MiB and a monitor error is the
// adapter's decision at the read boundary.
func ParseMetrics(body []byte) Metrics {
	var m Metrics
	if len(body) > MetricsBudget {
		return m
	}
	colos := map[string]bool{}
	for len(body) > 0 {
		var line []byte
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			line, body = body[:i], body[i+1:]
		} else {
			line, body = body, nil
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		name, i, ok := sampleName(line)
		// Read the name first, then skip an unwanted line before parsing its
		// labels: another exporter's family never allocates here.
		if !ok || !allowedFamilies[name] {
			continue
		}
		labels, raw, ok := sampleValue(line, i)
		if !ok {
			continue
		}
		value, ok := metricInteger(raw)
		if !ok {
			continue
		}
		m.parsed = true
		switch name {
		case "cloudflared_tunnel_server_locations":
			if value != 1 {
				continue
			}
			// The label is documented lowercase, while Cloudflare writes
			// colos uppercase elsewhere; normalize the report to the
			// documented lowercase fact shape either way.
			colo := strings.ToLower(labels["edge_location"])
			if coloToken.MatchString(colo) {
				colos[colo] = true
			}
		case "build_info":
			if version := labels["version"]; versionToken.MatchString(version) {
				m.ConnectorVersion = version
			}
		case "cloudflared_tunnel_ha_connections":
			m.HAConnections = count(value)
		case "cloudflared_tunnel_total_requests":
			m.TotalRequests = count(value)
		case "cloudflared_tunnel_request_errors":
			m.RequestErrors = count(value)
		case "cloudflared_tunnel_active_streams":
			m.ActiveStreams = count(value)
		case "cloudflared_tunnel_timer_retries":
			m.TimerRetries = count(value)
		}
	}
	if len(colos) > 0 {
		m.ServerLocations = make([]string, 0, min(len(colos), MaxColos))
		for colo := range colos {
			m.ServerLocations = append(m.ServerLocations, colo)
		}
		sort.Strings(m.ServerLocations)
		if len(m.ServerLocations) > MaxColos {
			m.ServerLocations = m.ServerLocations[:MaxColos]
		}
	}
	return m
}

// sampleName reads the family name and returns the index after it, so an
// unwanted line can be skipped before its labels are parsed.
func sampleName(line []byte) (string, int, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	start := i
	for i < len(line) && metricNameChar(line[i]) {
		i++
	}
	if i == start {
		return "", 0, false
	}
	return string(line[start:i]), i, true
}

// sampleValue reads the optional label block with escaped quotes and
// backslashes and the sample's value token. A trailing timestamp is ignored.
// The line's remaining text is never kept.
func sampleValue(line []byte, i int) (map[string]string, []byte, bool) {
	labels := map[string]string{}
	if i < len(line) && line[i] == '{' {
		var ok bool
		if i, ok = parseLabels(line, i+1, labels); !ok {
			return nil, nil, false
		}
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	start := i
	for i < len(line) && line[i] != ' ' && line[i] != '\t' && line[i] != '\r' {
		i++
	}
	if i == start {
		return nil, nil, false
	}
	return labels, line[start:i], true
}

// parseLabels reads `name="value",...}` from i and returns the index after
// the closing brace. Only the escapes Prometheus defines are accepted;
// anything else makes the whole line unreadable, so no partial label set is
// trusted.
func parseLabels(line []byte, i int, out map[string]string) (int, bool) {
	for {
		if len(out) >= maxMetricLabels {
			return 0, false
		}
		start := i
		for i < len(line) && labelNameChar(line[i]) {
			i++
		}
		if i == start || i >= len(line) || line[i] != '=' {
			return 0, false
		}
		name := string(line[start:i])
		i++
		if i >= len(line) || line[i] != '"' {
			return 0, false
		}
		i++
		var value strings.Builder
		closed := false
		for i < len(line) {
			c := line[i]
			if c == '"' {
				i++
				closed = true
				break
			}
			if c == '\\' {
				i++
				if i >= len(line) || value.Len() >= maxMetricLabelValue {
					return 0, false
				}
				switch line[i] {
				case '\\':
					value.WriteByte('\\')
				case '"':
					value.WriteByte('"')
				case 'n':
					value.WriteByte('\n')
				default:
					return 0, false
				}
				i++
				continue
			}
			if value.Len() >= maxMetricLabelValue {
				return 0, false
			}
			value.WriteByte(c)
			i++
		}
		if !closed || len(name) > maxMetricLabelName {
			return 0, false
		}
		out[name] = value.String()
		if i >= len(line) {
			return 0, false
		}
		switch line[i] {
		case ',':
			i++
		case '}':
			return i + 1, true
		default:
			return 0, false
		}
	}
}

// metricInteger accepts a non-negative integer within the eighteen-digit
// bound. Prometheus exporters switch a large counter to exponent form
// (`1e+06`), so an exponent whose value is integral is read too; a fractional
// value, a signed mantissa, NaN, Inf or an overflowing value is not, which
// keeps every fact a bounded decimal.
func metricInteger(b []byte) (int64, bool) {
	if len(b) == 0 || len(b) > 32 {
		return 0, false
	}
	e := bytes.IndexAny(b, "eE")
	if e < 0 && bytes.IndexByte(b, '.') < 0 {
		return plainInteger(b)
	}
	mantissa, power := b, 0
	if e >= 0 {
		if bytes.IndexAny(b[e+1:], "eE") >= 0 {
			return 0, false
		}
		var exponent []byte
		mantissa, exponent = b[:e], b[e+1:]
		sign := 1
		if len(exponent) > 0 && (exponent[0] == '+' || exponent[0] == '-') {
			if exponent[0] == '-' {
				sign = -1
			}
			exponent = exponent[1:]
		}
		// A larger exponent can only overflow or be fractional; bounding
		// its digits bounds the work.
		if len(exponent) == 0 || len(exponent) > 3 {
			return 0, false
		}
		for _, c := range exponent {
			if c < '0' || c > '9' {
				return 0, false
			}
			power = power*10 + int(c-'0')
		}
		power *= sign
	}
	dot := bytes.IndexByte(mantissa, '.')
	digits, fraction := mantissa, 0
	if dot >= 0 {
		if dot == 0 || dot == len(mantissa)-1 || bytes.IndexByte(mantissa[dot+1:], '.') >= 0 {
			return 0, false
		}
		fraction = len(mantissa) - dot - 1
		digits = make([]byte, 0, len(mantissa)-1)
		digits = append(append(digits, mantissa[:dot]...), mantissa[dot+1:]...)
	}
	if len(digits) == 0 {
		return 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	scale := power - fraction
	if scale < 0 {
		need := -scale
		if need > len(digits) {
			return 0, false
		}
		for _, c := range digits[len(digits)-need:] {
			if c != '0' {
				return 0, false
			}
		}
		digits, scale = digits[:len(digits)-need], 0
	}
	start := 0
	for start < len(digits) && digits[start] == '0' {
		start++
	}
	digits = digits[start:]
	if len(digits) == 0 {
		return 0, true
	}
	if len(digits)+scale > maxMetricIntegerDigits {
		return 0, false
	}
	var n int64
	for _, c := range digits {
		n = n*10 + int64(c-'0')
	}
	for i := 0; i < scale; i++ {
		n *= 10
	}
	return n, true
}

func plainInteger(b []byte) (int64, bool) {
	if len(b) == 0 || len(b) > maxMetricIntegerDigits {
		return 0, false
	}
	var n int64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

func count(value int64) *int64 { return &value }

func decimal(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func metricNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == ':'
}

func labelNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
