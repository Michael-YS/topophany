package tui

import (
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCollectorIngressIsShownInTheStatusRow(t *testing.T) {
	m := previewModel(t)
	// Render re-simulates a preview scenario every frame, which would replace
	// the snapshot this test edits; the row is the same code without it.
	m.Preview = nil
	m.Width, m.Height = 160, 45
	m.setTab(tabOverview)
	if out := visible(m); strings.Contains(out, "ingress") {
		t.Fatal("ingress shown without a failure")
	}
	m.Snapshot.Ingress = &model.Ingress{WindowSeconds: 1800, Failures: 3, Last: m.Snapshot.GeneratedAt,
		Sources: []model.IngressSource{{Address: "100.74.137.62", Count: 3, Last: m.Snapshot.GeneratedAt}}}
	if out := visible(m); !strings.Contains(out, "3 ingress") {
		t.Fatalf("the status row does not count collector ingress")
	}
	// The Overview keeps the row while delivery is failing; another tab
	// always has it.
	m.setTab(tabChecks)
	if out := visible(m); !strings.Contains(out, "3 ingress") {
		t.Fatalf("the status row lost ingress on another tab")
	}
}
