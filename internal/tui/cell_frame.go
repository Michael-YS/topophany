package tui

import (
	"strings"
	"unicode/utf8"
)

// CellFrame is a patch for the website's character-grid renderer. Styles are
// appended to the receiver's palette, unless Reset starts a new grid/palette.
// Each op is [row, column, text, [style index, cell count, ...]]. Text is a
// string, or a string array when a cell contains combining marks. U+0001 marks
// the second cell of a wide character; patches never split those pairs.
type CellFrame struct {
	Size   [2]int   `json:"size"`
	Reset  bool     `json:"reset"`
	Styles [][3]int `json:"styles"`
	Ops    [][]any  `json:"ops"`
}

type DemoFrame struct {
	CellFrame
	Title     string `json:"title"`
	Scenario  string `json:"scenario"`
	Observer  string `json:"observer"`
	Tab       int    `json:"tab"`
	Searching bool   `json:"searching"`
	Closed    bool   `json:"closed"`
	Text      string `json:"text,omitempty"`
	// Device is what the frame is laid out for ("fit" on a phone), or ""
	// for the terminal; the page sizes its cells to match.
	Device string `json:"device,omitempty"`
}

type cellEncoder struct {
	styles map[Style]int
	prev   *Canvas
}

func (e *cellEncoder) encode(cv *Canvas) CellFrame {
	f := CellFrame{Size: [2]int{cv.W, cv.H}, Ops: [][]any{}, Styles: [][3]int{}}
	if e.styles == nil || e.prev != nil && (e.prev.W != cv.W || e.prev.H != cv.H) {
		*e = cellEncoder{styles: map[Style]int{}}
		f.Reset = true
	}
	style := func(st Style) int {
		if st.BG == 0 {
			st.BG = cInk
		}
		if i, ok := e.styles[st]; ok {
			return i
		}
		i := len(e.styles)
		e.styles[st] = i
		fgc := -1
		if st.FG != 0 {
			fgc = int(st.FG & 0xFFFFFF)
		}
		f.Styles = append(f.Styles, [3]int{fgc, int(st.BG & 0xFFFFFF), int(st.Attr)})
		return i
	}
	for y := 0; y < cv.H; y++ {
		row := cv.cells[y*cv.W : (y+1)*cv.W]
		same := func(x int) bool { return e.prev != nil && e.prev.cells[y*cv.W+x] == row[x] }
		for x := 0; x < cv.W; {
			if same(x) {
				x++
				continue
			}
			// Close short unchanged gaps to avoid many tiny patches.
			end, gap := x, 0
			for i := x; i < cv.W && gap <= 6; i++ {
				if same(i) {
					gap++
					continue
				}
				end, gap = i+1, 0
			}
			start := x
			if row[start].cont && start > 0 {
				start--
			}
			if end < cv.W && row[end].cont {
				end++
			}
			parts := make([]string, 0, end-start)
			clustered := false
			runs := []int{}
			for i := start; i < end; i++ {
				p := row[i]
				switch {
				case p.cont:
					parts = append(parts, "\x01")
				default:
					parts = append(parts, p.ch)
					clustered = clustered || utf8.RuneCountInString(p.ch) != 1
				}
				s := style(p.st)
				if n := len(runs); n > 0 && runs[n-2] == s {
					runs[n-1]++
				} else {
					runs = append(runs, s, 1)
				}
			}
			var text any = strings.Join(parts, "")
			if clustered {
				text = parts
			}
			f.Ops = append(f.Ops, []any{y, start, text, runs})
			x = end
		}
	}
	// draw creates a fresh canvas each time; retain it for the next diff.
	e.prev = cv
	return f
}
