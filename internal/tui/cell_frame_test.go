package tui

import "testing"

// Decode the published protocol independently to catch lost cells, palette
// mismatches and split wide characters across consecutive frames.
func TestCellFramesReconstructCanvas(t *testing.T) {
	var encoder cellEncoder
	var chars []string
	var styles [][3]int
	var cellStyles []int
	frames := []*Canvas{NewCanvas(12, 3), NewCanvas(12, 3), NewCanvas(12, 3), NewCanvas(8, 2)}
	frames[0].Text(1, 0, "成都e\u0301", fg(cAccent), -1)
	frames[1].Text(2, 0, "東京", fg(cWarn).bold(), -1)
	frames[2].Text(2, 0, "X", fg(cDown), -1)
	frames[3].Text(0, 1, "日本", fg(cAccent2), -1)
	for n, cv := range frames {
		frame := encoder.encode(cv)
		if frame.Reset {
			chars, cellStyles = make([]string, cv.W*cv.H), make([]int, cv.W*cv.H)
			styles = nil
		}
		if frame.Reset != (n == 0 || n == 3) {
			t.Fatal("unexpected grid reset", n, frame.Reset)
		}
		styles = append(styles, frame.Styles...)
		for _, op := range frame.Ops {
			var tokens []string
			switch text := op[2].(type) {
			case string:
				for _, r := range text {
					tokens = append(tokens, string(r))
				}
			case []string:
				tokens = text
			}
			i := op[0].(int)*cv.W + op[1].(int)
			runs := op[3].([]int)
			run, left := 0, runs[1]
			for _, ch := range tokens {
				chars[i], cellStyles[i] = ch, runs[run]
				i++
				left--
				if left == 0 && run+2 < len(runs) {
					run += 2
					left = runs[run+1]
				}
			}
		}
		for i, c := range cv.cells {
			ch := c.ch
			if c.cont {
				ch = "\x01"
			}
			fgc, bg := -1, c.st.BG
			if c.st.FG != 0 {
				fgc = int(c.st.FG & 0xffffff)
			}
			if bg == 0 {
				bg = cInk
			}
			wantStyle := [3]int{fgc, int(bg & 0xffffff), int(c.st.Attr)}
			if chars[i] != ch || styles[cellStyles[i]] != wantStyle {
				t.Fatalf("frame %d cell %d: got %q %v, want %q %v", n, i, chars[i], styles[cellStyles[i]], ch, wantStyle)
			}
		}
		unchanged := encoder.encode(cv)
		if len(unchanged.Ops) != 0 || len(unchanged.Styles) != 0 || unchanged.Reset {
			t.Fatal("unchanged canvas emitted a patch", unchanged)
		}
	}
}
