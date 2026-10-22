package monitor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// These regressions were reconstructed from the retained audit scenarios.
func externalStatusConfig() Config {
	return Config{Template: Template{Schema: 1, Title: "Evidence", Rows: []Row{
		{ID: "check", Metric: "check.status", Visual: "text", Select: Selector{Check: "vm-resources"}},
		{ID: "summary", Metric: "summary.status", Visual: "text"},
	}}}
}

func statusPanelRoundTrip(t *testing.T, p Panel) Panel {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out Panel
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExternalCheckStatusUsesReportEvidence(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	in := externalPanelInput(now)
	p := Build(externalStatusConfig(), in, now)
	for _, panel := range []Panel{p, statusPanelRoundTrip(t, p)} {
		row := panel.Rows[0]
		if row.Value != "up" || row.Freshness != "fresh" || !row.SampledAt.Equal(now) {
			t.Fatalf("external status ignored accepted report: %+v", row)
		}
		if !row.ValidUntil.Equal(now.Add(model.SnapshotLifetime)) {
			t.Fatalf("status must respect snapshot lifetime: %+v", row)
		}
	}
}

func TestExternalMonitorRowsKeepReportDeadlines(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, clock := range []string{"contact", "data", "valid"} {
		t.Run(clock, func(t *testing.T) {
			in := externalPanelInput(now)
			deadline := now.Add(2 * time.Second)
			switch clock {
			case "contact":
				in.Snapshot.External[0].ContactDeadline = deadline
			case "data":
				in.Snapshot.External[0].DataDeadline = deadline
			case "valid":
				in.Snapshot.External[0].ValidUntil = deadline
			}
			before, _ := json.Marshal(in)
			p := Build(externalStatusConfig(), in, now)
			p.PublisherExpiresAt = now.Add(time.Minute)
			for _, row := range p.Rows {
				if row.Value == nil || !row.ValidUntil.Equal(deadline) {
					t.Fatalf("original deadline not carried: %+v", row)
				}
			}
			for _, panel := range []Panel{p, statusPanelRoundTrip(t, p)} {
				for _, row := range Current(panel, deadline).Rows {
					if row.Value != nil {
						t.Fatalf("expired evidence survived Current: %+v", row)
					}
				}
			}
			for _, row := range Build(externalStatusConfig(), in, deadline).Rows {
				if row.Value != nil {
					t.Fatalf("republishing refreshed old evidence: %+v", row)
				}
			}
			after, _ := json.Marshal(in)
			if string(before) != string(after) {
				t.Fatal("aging mutated the input snapshot")
			}
		})
	}
}

func TestExternalStatusMissingEvidenceCannotBeFresh(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, missing := range []string{"binding", "report", "sample", "data", "contact", "valid", "clock_ahead"} {
		t.Run(missing, func(t *testing.T) {
			in := externalPanelInput(now)
			b := &in.Snapshot.External[0]
			switch missing {
			case "binding":
				in.Snapshot.External = nil
			case "report":
				b.Report = nil
			case "sample":
				b.SampledAt = time.Time{}
			case "data":
				b.DataDeadline = time.Time{}
			case "contact":
				b.ContactDeadline = time.Time{}
			case "valid":
				b.ValidUntil = time.Time{}
			case "clock_ahead":
				b.SampledAt = now.Add(model.SampleClockAhead + time.Second)
			}
			for _, row := range Build(externalStatusConfig(), in, now).Rows {
				if row.Value != nil || row.Freshness == "fresh" {
					t.Fatalf("missing evidence remained fresh: %+v", row)
				}
			}
		})
	}
}

func TestSummaryStatusMissingNativeEvidenceCannotBeFresh(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	in := hostInput(now)
	in.Snapshot.Checks[0].Last = nil
	cfg := Config{Template: Template{Rows: []Row{{ID: "summary", Metric: "summary.status", Visual: "text"}}}}
	if row := Build(cfg, in, now).Rows[0]; row.Value != nil || row.Freshness == "fresh" {
		t.Fatalf("missing native evidence remained fresh: %+v", row)
	}
}

func TestSummaryStatusKeepsOriginalNativeEvidenceDeadline(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	in := externalPanelInput(now)
	in.Snapshot.External = nil
	in.Snapshot.Topology.Checks[0].Kind = "http"
	in.Snapshot.Topology.Checks[0].External = nil
	in.Snapshot.Topology.Checks[0].StaleSeconds = 2
	in.Snapshot.Checks[0].Last = &model.Observation{SampledAt: now}
	in.Snapshot.Checks[0].Evidence = "measured"
	p := Build(externalStatusConfig(), in, now)
	p.PublisherExpiresAt = now.Add(time.Minute)
	deadline := now.Add(2 * time.Second)
	if !p.Rows[1].ValidUntil.Equal(deadline) {
		t.Fatalf("summary deadline = %+v", p.Rows[1])
	}
	for _, panel := range []Panel{p, statusPanelRoundTrip(t, p)} {
		if row := Current(panel, deadline).Rows[1]; row.Value != nil {
			t.Fatalf("Current extended native deadline: %+v", row)
		}
	}
	if row := Build(externalStatusConfig(), in, deadline).Rows[1]; row.Value != nil {
		t.Fatalf("Build extended native deadline: %+v", row)
	}
}
