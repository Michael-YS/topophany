package admission_test

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
)

func TestClockRollbackAdvancesWithoutDoubleCounting(t *testing.T) {
	start := time.Now()
	var clock admission.Clock
	if got := clock.Advance(start, start); !got.Equal(start) {
		t.Fatal(got)
	}
	behind := start.Add(-time.Hour)
	for _, elapsed := range []time.Duration{time.Second, time.Minute, 10 * time.Minute} {
		want := start.Add(elapsed)
		if got := clock.Advance(behind, want); !got.Equal(want) {
			t.Fatalf("rollback froze clock: %v want %v", got, want)
		}
		if got := clock.Advance(behind, start); !got.Equal(want) {
			t.Fatal("delayed tick changed the high-water mark")
		}
		if got := clock.Advance(behind, want); !got.Equal(want) {
			t.Fatal("elapsed time was counted twice")
		}
	}
	forward := start.Add(2 * time.Hour)
	if got := clock.Advance(forward, start.Add(11*time.Minute)); !got.Equal(forward) {
		t.Fatal("forward wall-clock adjustment was ignored")
	}
}
