package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preview"
)

// term is just enough of a terminal to replay what the painter writes:
// cursor moves, SGR, hyperlinks, clears and printed runes.
type term struct {
	w, h  int
	ch    []string
	st    []Style
	links []string
	x, y  int
	cur   Style
	link  string
}

func newTerm(w, h int) *term {
	t := &term{w: w, h: h, ch: make([]string, w*h), st: make([]Style, w*h), links: make([]string, w*h)}
	t.clear()
	return t
}

func (t *term) clear() {
	for i := range t.ch {
		t.ch[i], t.st[i], t.links[i] = " ", Style{}, ""
	}
}

func (t *term) apply(tb *testing.T, s string) {
	tb.Helper()
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != 0x1b {
			k := runeWidth(rs[i])
			if k == 0 {
				continue
			}
			if t.x < t.w && t.y < t.h {
				at := t.y*t.w + t.x
				t.ch[at], t.st[at], t.links[at] = string(rs[i]), t.cur, t.link
				if k == 2 && t.x+1 < t.w {
					t.ch[at+1], t.st[at+1], t.links[at+1] = "", t.cur, t.link
				}
			}
			t.x += k
			continue
		}
		if i+1 < len(rs) && rs[i+1] == ']' {
			j := strings.Index(string(rs[i:]), "\x1b\\")
			osc, ok := strings.CutPrefix(string(rs[i:])[2:j], "8;;")
			if j < 0 || !ok {
				tb.Fatalf("unexpected OSC %q", string(rs[i:]))
			}
			t.link = osc
			i += len([]rune(string(rs[i:])[:j])) + 1
			continue
		}
		if i+1 >= len(rs) || rs[i+1] != '[' {
			tb.Fatalf("unexpected escape %q", string(rs[i:min(len(rs), i+8)]))
		}
		j := i + 2
		for j < len(rs) && (rs[j] < '@' || rs[j] > '~') {
			j++
		}
		params, final := string(rs[i+2:j]), rs[j]
		i = j
		switch final {
		case 'H':
			t.x, t.y = 0, 0
			if r, c, ok := strings.Cut(params, ";"); ok {
				ry, _ := strconv.Atoi(r)
				cx, _ := strconv.Atoi(c)
				t.x, t.y = cx-1, ry-1
			}
		case 'J':
			t.clear()
		case 'm':
			ps := strings.Split(params, ";")
			for k := 0; k < len(ps); k++ {
				switch ps[k] {
				case "0", "":
					t.cur = Style{}
				case "1", "2", "3", "4", "7":
					t.cur.Attr |= map[string]uint8{"1": Bold, "2": Faint, "3": Italic, "4": Underline, "7": Reverse}[ps[k]]
				case "38", "48":
					v := [3]uint32{}
					for n := range v {
						c, _ := strconv.Atoi(ps[k+2+n])
						v[n] = uint32(c)
					}
					if ps[k] == "38" {
						t.cur.FG = Hex(v[0]<<16 | v[1]<<8 | v[2])
					} else {
						t.cur.BG = Hex(v[0]<<16 | v[1]<<8 | v[2])
					}
					k += 4
				default:
					tb.Fatalf("unexpected SGR %q", params)
				}
			}
		case 'h', 'l':
		default:
			tb.Fatalf("unexpected control %q", params+string(final))
		}
	}
}

// matches compares what the terminal shows with the canvas, the way Lines
// would draw it: blanks by their background only.
func (t *term) matches(tb *testing.T, cv *Canvas) {
	tb.Helper()
	for y := 0; y < cv.H; y++ {
		for x := 0; x < cv.W; x++ {
			p := cv.at(x, y)
			if p.cont {
				continue
			}
			want := p.st
			if want.BG == 0 {
				want.BG = cv.Base
			}
			got, gs := t.ch[y*t.w+x], t.st[y*t.w+x]
			if got != p.ch || gs.BG != want.BG || p.ch != " " && (gs.FG != want.FG || gs.Attr != want.Attr) {
				tb.Fatalf("cell %d,%d: shows %q %+v, canvas has %q %+v", x, y, got, gs, p.ch, want)
			}
			if t.links[y*t.w+x] != cv.url(p) {
				tb.Fatalf("cell %d,%d links to %q, canvas to %q", x, y, t.links[y*t.w+x], cv.url(p))
			}
		}
	}
}

func TestScreenRepaintsOnlyWhatChanged(t *testing.T) {
	m := previewModel(t)
	m.Colors = colorTrue
	m.Hyperlinks = true
	m.Width, m.Height = 140, 40
	tm := newTerm(m.Width, m.Height)
	s := &screen{}
	step := func(what string) int {
		t.Helper()
		t.Log(what)
		cv := m.draw()
		f := s.frame(cv, "", colorTrue, m.Width, m.Height)
		tm.apply(t, f)
		tm.matches(t, cv)
		return len(f)
	}
	first := step("first frame")
	// An animation step on the map rewrites a few cells, not the screen.
	m.setTab(tabTopology)
	step("topology")
	m.Now = m.Now.Add(140 * time.Millisecond)
	if n := step("glint"); n == 0 || n > first/4 {
		t.Fatalf("a glint step wrote %d bytes; the first frame took %d", n, first)
	}
	if n := step("unchanged"); n != 0 {
		t.Fatalf("an unchanged frame wrote %d bytes", n)
	}
	// Tabs, scrolling, a modal over the dimmed screen, wide text replaced
	// by narrow and back, and the About animation and links all land
	// exactly.
	for _, k := range []string{"j", "j", "3", "j", "x", "esc", "4", "6", "8", "9"} {
		m.Key(k)
		m.Now = m.Now.Add(90 * time.Millisecond)
		step(k)
	}
	for i := 0; i < 5; i++ {
		m.Now = m.Now.Add(70 * time.Millisecond)
		step("about")
	}
	sim := m.Preview.(*preview.Sim)
	sim.SetScenario(1, m.Now.Add(-30*time.Minute))
	m.Snapshot, _ = sim.Snapshot("", m.Now)
	m.Key("1")
	step("scenario")
	// Too small, then back: the notice, then a full frame.
	m.Width, m.Height = 20, 5
	if f := s.frame(m.draw(), "small", colorTrue, 20, 5); !strings.Contains(f, "small") {
		t.Fatalf("too-small notice %q", f)
	}
	m.Width, m.Height = 140, 40
	tm = newTerm(140, 40)
	step("restored")
}

// A tint keeps a wide rune in one style even when the rectangle cuts it,
// and tints nothing twice.
func TestTintKeepsWideRunesWhole(t *testing.T) {
	cv := NewCanvas(10, 1)
	cv.Text(0, 0, "长沙ab長", fg(cText), -1) // 长 0-1, 沙 2-3, a 4, b 5, 長 6-7
	dim := func(s Style) Style { s.FG = Mix(s.FG, Hex(0), .5); return s }
	cv.Tint(3, 0, 4, 1, dim) // from the right half of 沙 to the left half of 長
	once := Mix(cText, Hex(0), .5)
	for x, want := range []Color{cText, cText, once, once, once, once, once, once, cText, cText} {
		if p := cv.at(x, 0); p.ch != " " && p.st.FG != want {
			t.Fatalf("cell %d %q: %v, want %v", x, p.ch, p.st.FG, want)
		}
	}
}
