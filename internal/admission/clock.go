package admission

import "time"

// Clock advances a trusted wall-clock high-water mark with monotonic elapsed
// time. Owners must serialize access. Worker timestamps are never inputs.
type Clock struct {
	high time.Time
	tick time.Time
}

func (c *Clock) Advance(wall, tick time.Time) time.Time {
	if c.tick.IsZero() || tick.After(c.tick) {
		if !c.tick.IsZero() {
			c.high = c.high.Add(tick.Sub(c.tick))
		}
		c.tick = tick
	}
	// A delayed clock read must not move the tick backwards: replaying it
	// would otherwise count the same elapsed interval twice on the next call.
	if wall.After(c.high) {
		c.high = wall
	}
	return c.high
}
