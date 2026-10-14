package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Moments in the evidence read the same everywhere: local time to the
// second, and for an incident's start, how long ago that was.
func TestEvidenceTimesAreReadable(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	since := now.Add(-125 * time.Second)
	if got, want := Stamp(now, since), since.Local().Format("2006-01-02 15:04:05 MST")+" · 2m ago"; got != want {
		t.Fatalf("stamp %q, want %q", got, want)
	}
	if Stamp(now, time.Time{}) != "never" {
		t.Fatal("a zero time is not never")
	}
	details := strings.Join(IncidentDetails(now, model.Incident{Scope: "home/path/p", Since: since}), "\n")
	if !strings.Contains(details, "Since: "+Stamp(now, since)) || strings.Contains(details, "T"+since.Local().Format("15:04:05")) {
		t.Fatalf("incident start is not readable:\n%s", details)
	}
}
