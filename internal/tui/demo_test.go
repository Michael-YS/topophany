package tui

import (
	"strings"
	"testing"
	"time"
)

func demoAt(t *testing.T) (*Demo, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	d, err := NewDemo("zh", now)
	if err != nil {
		t.Fatal(err)
	}
	f := d.Frame(now, 120, 36, true)
	if !f.Reset || !strings.Contains(f.Text, "成都") || f.Observer != "obs-mac" {
		t.Fatal("preview did not open on the home observer", f)
	}
	return d, now
}

func TestDemoFaultEvidenceVantageAndRecovery(t *testing.T) {
	d, now := demoAt(t)
	d.SetScenario("transit-blocked", now)
	now = now.Add(90 * time.Second)
	d.Frame(now, 120, 36, true)
	if len(d.m.Snapshot.Incidents) == 0 {
		t.Fatal("confirmed failure has no incidents")
	}
	d.Key("enter", now)
	f := d.Frame(now, 120, 36, true)
	if !d.m.Detail || !strings.Contains(f.Text, "EVIDENCE") {
		t.Fatal("incident evidence did not open", f.Text)
	}
	d.Key("esc", now)
	for range 20 {
		if d.m.Observer == "obs-app" {
			break
		}
		d.Key("v", now)
		d.Frame(now, 120, 36, true)
	}
	if d.m.Observer != "obs-app" || len(d.m.Snapshot.Incidents) != 0 {
		t.Fatal("overseas observer did not show its independent view", d.m.Observer, d.m.Snapshot.Incidents)
	}
	for d.m.Observer != "obs-mac" {
		d.Key("v", now)
		d.Frame(now, 120, 36, true)
	}
	d.SetScenario("normal", now)
	now = now.Add(2 * time.Minute)
	d.Frame(now, 120, 36, true)
	for _, incident := range d.m.Snapshot.Ledger {
		if !incident.Closed.IsZero() {
			return
		}
	}
	t.Fatal("recovery lost the closed incident history")
}

func TestDemoUsesOneClockForScenarioMenuAndHistory(t *testing.T) {
	d, now := demoAt(t)
	d.Key("x", now)
	d.Frame(now, 120, 36, true)
	d.Key("2", now)
	f := d.Frame(now.Add(90*time.Second), 120, 36, true)
	if f.Scenario != "transit-blocked" || len(d.m.Snapshot.Incidents) == 0 {
		t.Fatal("scenario menu used wall time instead of the demo clock")
	}
	d.Key("6", now.Add(90*time.Second))
	d.Frame(now.Add(90*time.Second), 120, 36, true)
	selectFirstEntry(t, d.m)
	d.Frame(now.Add(90*time.Second), 120, 36, true)
	if len(d.m.samples.entries) == 0 {
		t.Fatal("selected check has no sample history")
	}
}

func TestDemoInputResizeIdleAndQuit(t *testing.T) {
	d, now := demoAt(t)
	if d.Frame(now.Add(time.Millisecond), 120, 36, false) != nil {
		t.Fatal("idle demo painted before its animation interval")
	}
	d.Key("3", now)
	d.Frame(now, 120, 36, true)
	d.Key("/", now)
	d.Key("paste:成都", now)
	f := d.Frame(now, 80, 24, true)
	if !f.Reset || !f.Searching || d.m.Search != "成都" || !strings.Contains(f.Text, "成都") {
		t.Fatal("resize/search lost state", f)
	}
	d.Key("esc", now)
	d.Key("q", now)
	f = d.Frame(now, 80, 24, true)
	if !f.Closed {
		t.Fatal("quit did not end the session")
	}
	if d.Frame(now.Add(time.Hour), 80, 24, false) != nil {
		t.Fatal("closed session kept running")
	}
}
