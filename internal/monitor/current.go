package monitor

import "time"

// Current is the reader-side freshness policy. A producer cannot update its
// file after a crash, so every file consumer must apply deadlines locally.
func Current(p Panel, now time.Time) Panel {
	p = clone(p)
	if p.Schema != 1 {
		p.Lifecycle = "unavailable"
	}
	if p.Lifecycle == "running" && (!now.Before(p.PublisherExpiresAt) || p.GeneratedAt.After(now.Add(5*time.Second))) {
		p.Lifecycle = "publisher_expired"
	}
	for i := range p.Rows {
		r := p.Rows[i]
		if p.Lifecycle != "running" {
			p.Rows[i] = missing(r, p.Lifecycle)
		} else if r.Value != nil && !now.Before(r.ValidUntil) {
			r.Freshness = "stale"
			p.Rows[i] = missing(r, "sample_expired")
		}
	}
	return p
}
