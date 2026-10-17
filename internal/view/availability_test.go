package view

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestUnreconciledRollupsDoNotClaimCompleteUptime(t *testing.T) {
	r := model.Rollup{Seconds: 60, Up: 60, Samples: 2, Unreconciled: 1}
	if UpShare(r) != "incomplete" || !strings.Contains(RollupLine(r), "late samples not reconciled") || strings.Contains(RollupLine(r), "100%") {
		t.Fatal(RollupLine(r))
	}
	if got := Up24h([]model.Availability{{Window: "24h", Rollup: r}}); got != "incomplete" {
		t.Fatal(got)
	}
}

func TestUp24hSaysWhenTheDayIsNotWhole(t *testing.T) {
	now := time.Date(2026, 10, 17, 13, 0, 0, 0, time.UTC)
	full := []model.Availability{{Window: "24h", Rollup: model.Rollup{Seconds: 86400, Up: 86400, Samples: 12}}}
	if got := Up24h(full); got != "100%" {
		t.Fatalf("a whole day gained a coverage note: %q", got)
	}
	part := []model.Availability{{Window: "24h", Rollup: model.Rollup{Seconds: 86400, Up: 3 * 3600, Samples: 12}, Since: now.Add(-3 * time.Hour)}}
	if got := Up24h(part); got != "100% · 3h" {
		t.Fatalf("partial day did not say how much was measured: %q", got)
	}
	part[0].Rollup = model.Rollup{Seconds: 86400, Up: 47 * 60, Samples: 12}
	if got := Up24h(part); got != "100% · 47m" {
		t.Fatalf("minutes: %q", got)
	}
	// The first sample can be old while most of the window is a gap. The
	// note has to follow measured time, not the series start.
	gapped := []model.Availability{{Window: "24h", Rollup: model.Rollup{Seconds: 86400, Up: 3600, Unknown: 82800}, Since: now.Add(-23 * time.Hour)}}
	if got := Up24h(gapped); got != "100% · 1h" {
		t.Fatalf("a gap was presented as coverage: %q", got)
	}
	empty := []model.Availability{{Window: "24h", Rollup: model.Rollup{Seconds: 86400}, Since: now.Add(-time.Hour)}}
	if got := Up24h(empty); got != "" {
		t.Fatalf("nothing measured gained a share: %q", got)
	}
	if got := Up24h(nil); got != "" {
		t.Fatal(got)
	}
}
