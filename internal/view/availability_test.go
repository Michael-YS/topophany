package view

import (
	"strings"
	"testing"

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
