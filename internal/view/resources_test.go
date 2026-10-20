package view_test

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestServerCardsShowOnlyCurrentLocalResources(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	c, err := preview.Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := preview.New(c, now, "en").Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	v := view.View{Snapshot: snap, Now: now}
	resources := v.Fleet().Card("app").Resources
	if len(resources) != 1 || resources[0].Check != "host-app" || resources[0].Health != "up" {
		t.Fatal(resources)
	}
	if len(v.Fleet().Card("cf").Resources) != 0 {
		t.Fatal("edge has local host resources")
	}
	details := ""
	for _, r := range v.Rows(view.Servers) {
		if r.ID == "app" {
			details = strings.Join(r.Details, "\n")
		}
	}
	if !strings.Contains(details, "Local resources:") || !strings.Contains(details, "Memory:") {
		t.Fatal(details)
	}
	for _, mode := range []string{"cached", "expired", "error", "stale", "future"} {
		copy := v
		copy.Snapshot.Checks = append(copy.Snapshot.Checks[:0:0], snap.Checks...)
		for i := range copy.Snapshot.Checks {
			st := &copy.Snapshot.Checks[i]
			if st.ID != "host-app" {
				continue
			}
			last := *st.Last
			st.Last = &last
			switch mode {
			case "cached":
				copy.Cached = true
			case "expired":
				copy.Now = now.Add(time.Minute)
			case "error":
				st.Last.Outcome = "error"
			case "stale":
				st.Freshness = "stale"
			case "future":
				st.Last.SampledAt = now.Add(time.Hour)
			}
		}
		r := copy.Fleet().Card("app").Resources[0]
		if r.Health != "unknown" || len(r.Facts) > 0 {
			t.Fatal("historical values shown as current", mode, r)
		}
		for _, pair := range view.ResourceValues(r) {
			if pair[1] != "unknown" {
				t.Fatal(mode, pair)
			}
		}
	}
}
