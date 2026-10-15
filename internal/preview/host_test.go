package preview

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
)

func TestHostPreviewLeavesUnsimulatedCountersUnknown(t *testing.T) {
	c, err := config.Load("../../examples/host-resources.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s := New(c, now.Add(-time.Minute), DefaultLanguage)
	snap, err := s.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	st := check(t, snap, "host-resources")
	if st.Health != "unknown" || st.Last == nil || st.Last.Evidence != "reported" || st.Last.Code != "host_preview_unavailable" || st.Last.Connectivity {
		t.Fatal(st)
	}
	if snap.Nodes[0].Health != "unknown" {
		t.Fatal("local telemetry proved remote availability", snap.Nodes)
	}
}
