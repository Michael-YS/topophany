package view

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const noInference = "No inference about all IP ports, untested UDP, physical power or GFW authority."

// incidentLines are the details of the incident the engine opens from the
// samples, from "Failed checks:" on.
func incidentLines(t *testing.T, c model.Config, now time.Time, samples ...model.Observation) []string {
	t.Helper()
	v := View{Snapshot: engine.Build(c, "primary", "", samples, nil, now), Now: now}
	rows := v.Rows(Incidents)
	if len(rows) != 1 {
		t.Fatalf("%d incident rows, want 1", len(rows))
	}
	d := rows[0].Details
	at := slices.Index(d, "Failed checks:")
	if at < 0 {
		t.Fatalf("no failed checks in %q", d)
	}
	return d[at:]
}

// A policy incident has no other observer to contrast and no hypothesis the
// engine draws, so the view leaves those headings out and says, under its own
// heading, what the sample observed, with the colo it reported and the list it
// was held to.
func TestPolicyIncidentSaysWhatWasViolated(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	policy := func(c model.Config, code, stage, detail string, facts map[string]string) model.Observation {
		o := testkit.Observation(c, "home-http", 1, now.Add(-time.Second), "policy")
		o.Code, o.Stage, o.Detail = code, stage, detail
		for k, v := range facts {
			o.Facts[k] = v
		}
		return o
	}
	for _, tc := range []struct {
		name  string
		setup func(*model.Check)
		code  string
		stage string
		facts map[string]string
		line  string
	}{
		{"pop mismatch", func(x *model.Check) { x.HTTP.ExpectedPops = []string{"HKG", "NRT"} },
			"pop_mismatch", "edge_assert", map[string]string{"edge_provider": "cloudflare", "edge_pop": "LAX"},
			"request succeeded but the edge answered from a colo outside the declared list (answered from LAX; declared HKG, NRT)"},
		{"pop mismatch with the colo withheld", func(x *model.Check) { x.HTTP.ExpectedPops = []string{"HKG", "NRT"} },
			"pop_mismatch", "edge_assert", map[string]string{"edge_provider": "cloudflare"},
			"request succeeded but the edge answered from a colo outside the declared list (declared HKG, NRT)"},
		{"trace colo mismatch", func(x *model.Check) {
			x.HTTP.ExitFormat = "trace"
			x.HTTP.ExpectedTrace = &model.TraceExpectations{Colos: []string{"SJC", "SEA"}}
		}, "colo_mismatch", "trace_assert", map[string]string{"trace_colo": "LAX"},
			"request succeeded but the edge answered from a colo outside the declared list (answered from LAX; declared SJC, SEA)"},
		{"warp mismatch", func(x *model.Check) {
			x.HTTP.ExitFormat = "trace"
			x.HTTP.ExpectedTrace = &model.TraceExpectations{Warp: "on"}
		}, "warp_mismatch", "trace_assert", map[string]string{"trace_warp": "off"},
			"request succeeded but the trace reports WARP outside the declared policy (WARP off; declared on)"},
		{"gateway mismatch", func(x *model.Check) {
			x.HTTP.ExitFormat = "trace"
			x.HTTP.ExpectedTrace = &model.TraceExpectations{Gateway: "off"}
		}, "gateway_mismatch", "trace_assert", map[string]string{"trace_gateway": "on"},
			"request succeeded but the trace reports Gateway outside the declared policy (Gateway on; declared off)"},
		// The older exit scenario: the sample's own sentence, nothing added.
		{"exit mismatch", func(x *model.Check) { x.HTTP.ExpectedExit = []string{"203.0.113.40"} },
			"exit_mismatch", "exit_assert", map[string]string{"exit_ip": "203.0.113.99"},
			"request succeeded but observed exit violates the declared policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			tc.setup(&c.Checks[0])
			detail := tc.line
			if i := strings.LastIndex(detail, " ("); i >= 0 {
				detail = detail[:i]
			}
			got := incidentLines(t, c, now, policy(c, tc.code, tc.stage, detail, tc.facts))
			want := []string{"Failed checks:", "home-http", "", "Policy (not an outage):", tc.line, "", noInference}
			if !slices.Equal(got, want) {
				t.Fatalf("details\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// A confirmed failure keeps the sections the engine fills, headings and all.
func TestFailedIncidentKeepsItsSections(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := testkit.Config()
	var samples []model.Observation
	for seq := uint64(1); seq <= 3; seq++ {
		at := now.Add(-time.Duration(4-seq) * time.Second)
		samples = append(samples, testkit.Observation(c, "home-http", seq, at, "fail"), testkit.Observation(c, "cloud-http", seq, at, "ok"))
	}
	got := incidentLines(t, c, now, samples...)
	want := []string{
		"Failed checks:", "home-http", "",
		"Other-observer response evidence:", "cloud-http", "",
		"Hypothesis (not a finding):", "source/path-specific interference, filtering or routing fault is possible; blocking authority and untested protocols are not established", "",
		noInference,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("details\n%q\nwant\n%q", got, want)
	}
}

// A section with nothing under it has no heading, whoever builds the details.
func TestIncidentDetailsOmitEmptySections(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	got := IncidentDetails(now, model.Incident{Scope: "home/home-http/policy", Summary: "observed policy constraint violation", Checks: []string{"a", "b"}}, nil)
	for _, line := range got {
		if strings.HasPrefix(line, "Other-observer") || strings.HasPrefix(line, "Hypothesis") {
			t.Fatalf("empty section %q has a heading in %q", line, got)
		}
	}
	got = IncidentDetails(now, model.Incident{Scope: "home/home-http", Checks: []string{"a"}, Hypothesis: "one\ntwo"}, nil)
	at := slices.Index(got, "Hypothesis (not a finding):")
	if at < 0 || !slices.Equal(got[at+1:at+3], []string{"one", "two"}) || slices.Contains(got, "Policy (not an outage):") {
		t.Fatalf("a hypothesis of two lines: %q", got)
	}
	// What a policy incident says is observed, so it is not a hypothesis.
	got = IncidentDetails(now, model.Incident{Scope: "home/home-http/policy", Checks: []string{"a"}, Hypothesis: "one\ntwo"}, nil)
	at = slices.Index(got, "Policy (not an outage):")
	if at < 0 || !slices.Equal(got[at+1:at+3], []string{"one", "two"}) || slices.Contains(got, "Hypothesis (not a finding):") {
		t.Fatalf("a policy incident of two lines: %q", got)
	}
}
