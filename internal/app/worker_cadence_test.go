package app

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestIdleHeartbeatIntervalFollowsObserverFreshness(t *testing.T) {
	c := testkit.Config()
	for _, seconds := range []int{5, 15, 90, 86400} {
		c.Observers[0].StaleSeconds = seconds
		for _, requested := range []time.Duration{time.Second, 15 * time.Second, time.Minute} {
			got := idleHeartbeatInterval(c, c.Observers[0].ID, requested)
			if got > requested || got*3 > time.Duration(seconds)*time.Second || got <= 0 {
				t.Fatalf("stale=%d requested=%s got=%s", seconds, requested, got)
			}
		}
	}
}
