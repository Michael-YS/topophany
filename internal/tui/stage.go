package tui

// rect is a rectangle of cells.
type rect struct{ x, y, w, h int }

func (r rect) right() int             { return r.x + r.w }
func (r rect) bottom() int            { return r.y + r.h }
func (r rect) empty() bool            { return r.w <= 0 || r.h <= 0 }
func (r rect) contains(x, y int) bool { return x >= r.x && x < r.right() && y >= r.y && y < r.bottom() }
func (r rect) overlaps(o rect) bool {
	return !r.empty() && !o.empty() && r.x < o.right() && o.x < r.right() && r.y < o.bottom() && o.y < r.bottom()
}
func (r rect) cut(top, bottom int) rect { return rect{r.x, r.y + top, r.w, max(0, r.h-top-bottom)} }

// stageOn is where the TUI draws on cv: the page of a device's screen
// while one is drawn, otherwise all of cv.
func (m *Model) stageOn(cv *Canvas) rect {
	if m.stage != nil {
		return *m.stage
	}
	return rect{0, 0, cv.W, cv.H}
}
