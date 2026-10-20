package cloudflared

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "2026.10.0", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func countOf(t *testing.T, value *int64, want int64) {
	t.Helper()
	if value == nil || *value != want {
		t.Fatalf("want count %d; got %v", want, value)
	}
}

func TestParseMetricsLiveFixture(t *testing.T) {
	m := ParseMetrics(readFixture(t, "metrics.txt"))
	if !m.Parsed() {
		t.Fatal("live metrics body was not readable")
	}
	countOf(t, m.HAConnections, 1)
	countOf(t, m.TotalRequests, 0)
	countOf(t, m.RequestErrors, 0)
	// The live unready capture has no connection, so the location and stream
	// families are absent.
	if m.ActiveStreams != nil || m.TimerRetries != nil || len(m.ServerLocations) != 0 {
		t.Fatalf("unexpected families in unready capture: %+v", m)
	}
	if m.ConnectorVersion != "2026.10.0" {
		t.Fatalf("connector version %q", m.ConnectorVersion)
	}
}

func TestParseMetricsConnectedFixture(t *testing.T) {
	m := ParseMetrics(readFixture(t, "metrics-connected.synthetic.txt"))
	if !m.Parsed() {
		t.Fatal("synthetic body was not readable")
	}
	countOf(t, m.HAConnections, 4)
	countOf(t, m.TotalRequests, 120)
	countOf(t, m.RequestErrors, 2)
	countOf(t, m.ActiveStreams, 3)
	countOf(t, m.TimerRetries, 1)
	if m.ConnectorVersion != "2026.10.0" {
		t.Fatalf("connector version %q", m.ConnectorVersion)
	}
	if want := []string{"hkg", "lax", "nrt", "sin"}; !slices.Equal(m.ServerLocations, want) {
		t.Fatalf("colos %v; want %v", m.ServerLocations, want)
	}
}

func TestParseMetricsAllowlistAndValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		check func(*testing.T, Metrics)
	}{
		{
			name: "unrelated families skipped",
			body: "go_goroutines 24\ncloudflared_tunnel_response_by_code{status_code=\"200\"} 3\ncloudflared_tunnel_total_requests 7\n",
			check: func(t *testing.T, m Metrics) {
				countOf(t, m.TotalRequests, 7)
			},
		},
		{
			name: "bucket sum count skipped",
			body: "cloudflared_tunnel_server_locations_bucket{edge_location=\"hkg\",le=\"1\"} 1\ncloudflared_tunnel_ha_connections 2\n",
			check: func(t *testing.T, m Metrics) {
				countOf(t, m.HAConnections, 2)
				if len(m.ServerLocations) != 0 {
					t.Fatal("histogram members read as the family")
				}
			},
		},
		{
			name: "escaped labels",
			body: `cloudflared_tunnel_server_locations{connection_id="a\"b\\c",edge_location="hkg"} 1` + "\n",
			check: func(t *testing.T, m Metrics) {
				if !slices.Equal(m.ServerLocations, []string{"hkg"}) {
					t.Fatal(m.ServerLocations)
				}
			},
		},
		{
			name: "uppercase edge location normalized",
			body: "cloudflared_tunnel_server_locations{edge_location=\"HKG\"} 1\n",
			check: func(t *testing.T, m Metrics) {
				if !slices.Equal(m.ServerLocations, []string{"hkg"}) {
					t.Fatal(m.ServerLocations)
				}
			},
		},
		{
			name: "zero and malformed locations ignored",
			body: strings.Join([]string{
				`cloudflared_tunnel_server_locations{edge_location="hkg"} 0`,
				`cloudflared_tunnel_server_locations{edge_location="sin"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="HK"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="hkg-1"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="hkg\t"} 1`,
				"",
			}, "\n"),
			check: func(t *testing.T, m Metrics) {
				if !slices.Equal(m.ServerLocations, []string{"sin"}) {
					t.Fatal(m.ServerLocations)
				}
			},
		},
		{
			name: "duplicates and more than eight",
			body: strings.Join([]string{
				`cloudflared_tunnel_server_locations{edge_location="hkg"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="hkg"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="sin"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="nrt"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="lax"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="sfo"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="sea"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="ord"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="iad"} 1`,
				`cloudflared_tunnel_server_locations{edge_location="jfk"} 1`,
				"",
			}, "\n"),
			check: func(t *testing.T, m Metrics) {
				if len(m.ServerLocations) != MaxColos {
					t.Fatalf("got %v", m.ServerLocations)
				}
				if !slices.IsSorted(m.ServerLocations) || m.ServerLocations[0] != "hkg" || m.ServerLocations[MaxColos-1] != "sfo" {
					t.Fatalf("got %v", m.ServerLocations)
				}
				seen := map[string]bool{}
				for _, colo := range m.ServerLocations {
					if seen[colo] {
						t.Fatal("duplicate colo")
					}
					seen[colo] = true
				}
			},
		},
		{
			name: "version bounds",
			body: "build_info{version=\"2026.10.0\"} 1\n",
			check: func(t *testing.T, m Metrics) {
				if m.ConnectorVersion != "2026.10.0" {
					t.Fatal(m.ConnectorVersion)
				}
			},
		},
		{
			name: "version over budget omitted",
			body: `build_info{version="` + strings.Repeat("9", maxConnectorVersion+1) + `"} 1` + "\n",
			check: func(t *testing.T, m Metrics) {
				if m.ConnectorVersion != "" {
					t.Fatal(m.ConnectorVersion)
				}
				if !m.Parsed() {
					t.Fatal("family was not parsed")
				}
			},
		},
		{
			name: "version shape rejected",
			body: `build_info{version="2026.10.0 \"quoted\""} 1` + "\n",
			check: func(t *testing.T, m Metrics) {
				if m.ConnectorVersion != "" {
					t.Fatal(m.ConnectorVersion)
				}
			},
		},
		{
			name: "unknown escape rejects the line",
			body: `cloudflared_tunnel_ha_connections{label="a\qb"} 3` + "\n" + "cloudflared_tunnel_ha_connections 4\n",
			check: func(t *testing.T, m Metrics) {
				countOf(t, m.HAConnections, 4)
			},
		},
		{
			name: "non-integer values skipped",
			body: "cloudflared_tunnel_total_requests 1.5\ncloudflared_tunnel_request_errors -1\ncloudflared_tunnel_active_streams +2\ncloudflared_tunnel_timer_retries 9999999999999999999\ncloudflared_tunnel_ha_connections 0\n",
			check: func(t *testing.T, m Metrics) {
				if m.TotalRequests != nil || m.RequestErrors != nil || m.ActiveStreams != nil || m.TimerRetries != nil {
					t.Fatalf("non-integer read as a count: %+v", m)
				}
				countOf(t, m.HAConnections, 0)
			},
		},
		{
			name: "timestamp ignored",
			body: "cloudflared_tunnel_ha_connections 2 1729382400000\n",
			check: func(t *testing.T, m Metrics) {
				countOf(t, m.HAConnections, 2)
			},
		},
		{
			name: "crlf and comments",
			body: "# HELP cloudflared_tunnel_ha_connections connections\r\n# TYPE cloudflared_tunnel_ha_connections gauge\r\ncloudflared_tunnel_ha_connections 4\r\n",
			check: func(t *testing.T, m Metrics) {
				countOf(t, m.HAConnections, 4)
			},
		},
		{
			name: "empty and foreign",
			body: "go_goroutines 24\nprocess_cpu_seconds_total 1\n",
			check: func(t *testing.T, m Metrics) {
				if m.Parsed() {
					t.Fatal("foreign body read as telemetry")
				}
			},
		},
		{
			name: "oversized body",
			body: "cloudflared_tunnel_ha_connections 4\n" + strings.Repeat("#", MetricsBudget),
			check: func(t *testing.T, m Metrics) {
				if m.Parsed() {
					t.Fatal("oversized body parsed")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, ParseMetrics([]byte(tc.body)))
		})
	}
}

func FuzzMetrics(f *testing.F) {
	for _, seed := range []string{
		string(readFixture(f, "metrics.txt")),
		string(readFixture(f, "metrics-connected.synthetic.txt")),
		"cloudflared_tunnel_server_locations{edge_location=\"hkg\"} 1\n",
		`cloudflared_tunnel_server_locations{connection_id="a\"b",edge_location="sin"} 1`,
		"build_info{version=\"2026.10.0\"} 1",
		"cloudflared_tunnel_ha_connections{",
		"cloudflared_tunnel_ha_connections{} 1",
		`cloudflared_tunnel_ha_connections{a="` + strings.Repeat("x", maxMetricLabelValue+1) + `"} 1`,
		"cloudflared_tunnel_total_requests 999999999999999999",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		m := ParseMetrics(body)
		if len(m.ServerLocations) > MaxColos {
			t.Fatal("too many colos")
		}
		if !slices.IsSorted(m.ServerLocations) {
			t.Fatal("colos unsorted")
		}
		for i, colo := range m.ServerLocations {
			if !coloToken.MatchString(colo) || strings.ToLower(colo) != colo || i > 0 && m.ServerLocations[i-1] == colo {
				t.Fatalf("colo escaped its shape: %q", colo)
			}
		}
		if m.ConnectorVersion != "" && (!versionToken.MatchString(m.ConnectorVersion) || len(m.ConnectorVersion) > maxConnectorVersion) {
			t.Fatalf("version escaped its shape: %q", m.ConnectorVersion)
		}
		for _, p := range []*int64{m.HAConnections, m.TotalRequests, m.RequestErrors, m.ActiveStreams, m.TimerRetries} {
			if p != nil && *p < 0 {
				t.Fatal("negative count")
			}
		}
	})
}

func TestMetricIntegerForms(t *testing.T) {
	for _, tc := range []struct {
		token string
		want  int64
		ok    bool
	}{
		{"0", 0, true},
		{"999999", 999999, true},
		{"1000000", 1000000, true},
		{"1e+06", 1000000, true},
		{"1E6", 1000000, true},
		{"1.0e+06", 1000000, true},
		{"1.000001e+06", 1000001, true},
		{"2.0", 2, true},
		{"9.9e+17", 990000000000000000, true},
		{"1.5", 0, false},
		{"1.0000015e+06", 0, false},
		{"1e-1", 0, false},
		{"1e+19", 0, false},
		{"1000000000000000000", 0, false},
		{"9.9e+18", 0, false},
		{"-1", 0, false},
		{"+1", 0, false},
		{"+1e3", 0, false},
		{"NaN", 0, false},
		{"Inf", 0, false},
		{"+Inf", 0, false},
		{"1e", 0, false},
		{"e6", 0, false},
		{"E6", 0, false},
		{"1e+", 0, false},
		{"1.", 0, false},
		{".5", 0, false},
		{"1e+0006", 0, false},
	} {
		t.Run(tc.token, func(t *testing.T) {
			got, ok := metricInteger([]byte(tc.token))
			if ok != tc.ok || ok && got != tc.want {
				t.Fatalf("got %d, %v; want %d, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestParseMetricsExponentCounter(t *testing.T) {
	m := ParseMetrics([]byte("cloudflared_tunnel_total_requests 1e+06\ncloudflared_tunnel_request_errors 1.000001e+06\n"))
	countOf(t, m.TotalRequests, 1000000)
	countOf(t, m.RequestErrors, 1000001)
}

// An escaped byte counts against the label-value budget too: a long run of
// escaped quotes cannot allocate past 256 bytes, and the line is skipped
// while later lines still parse.
func TestParseMetricsLabelValueBound(t *testing.T) {
	within := strings.Repeat(`\"`, maxMetricLabelValue)
	m := ParseMetrics([]byte(`cloudflared_tunnel_ha_connections{label="` + within + `"} 4` + "\n"))
	countOf(t, m.HAConnections, 4)

	over := strings.Repeat(`\"`, maxMetricLabelValue+1)
	m = ParseMetrics([]byte(`cloudflared_tunnel_ha_connections{label="` + over + `"} 4` + "\ncloudflared_tunnel_ha_connections 5\n"))
	countOf(t, m.HAConnections, 5)
}
