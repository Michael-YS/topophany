package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/preview"
)

// The homepage (site/public) replays the real TUI: `make site` drives a preview
// model through the tour below, once in each language the preview is written
// in, and writes every frame as the cells that changed since the one before
// (tour.<language>.json), with the land mask the hero globe is drawn from.
// Without TOPO_SITE the test is skipped.

const tourW, tourH = 140, 42

type tourFrame struct {
	D   int     `json:"d"`   // milliseconds this frame stays on screen
	Ops [][]any `json:"ops"` // [y, x, text, [style, count, ...]]
}

type tourChapter struct {
	ID     string      `json:"id"`
	Tab    int         `json:"tab"`
	Frames []tourFrame `json:"frames"`
}

type tour struct {
	Size     [2]int        `json:"size"`
	Styles   [][3]int      `json:"styles"` // fg (-1 default), bg, attributes
	Tabs     [][2]int      `json:"tabs"`   // x, width of each tab on row 1
	Names    []string      `json:"names"`  // the name of each tab
	Chapters []tourChapter `json:"chapters"`
}

type recorder struct {
	t       *testing.T
	m       *Model
	sim     *preview.Sim
	fetched time.Time
	styles  map[Style]int
	out     tour
	prev    []cell
}

func newRecorder(t *testing.T, lang string) *recorder {
	c, e := preview.Preset(lang)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sim := preview.New(c, now, lang)
	r := &recorder{t: t, sim: sim, styles: map[Style]int{}}
	r.m = &Model{Now: now, Width: tourW, Height: tourH, Preview: sim, Server: "synthetic", Colors: colorTrue}
	r.out.Size = [2]int{tourW, tourH}
	r.out.Names = tabs
	r.fetch()
	r.m.say("Preview: synthetic evidence only · x switch scenario · D debug overlay", false)
	return r
}

func (r *recorder) fetch() {
	snap, e := r.sim.Snapshot(r.m.Observer, r.m.Now)
	if e != nil {
		r.t.Fatal(e)
	}
	r.m.Snapshot, r.fetched = snap, r.m.Now
}

// chapter starts a new chapter with a full frame, so the page can jump to it.
func (r *recorder) chapter(id string) {
	r.out.Chapters = append(r.out.Chapters, tourChapter{ID: id, Tab: r.m.Tab})
	r.prev = nil
}

func (r *recorder) style(st Style) int {
	if st.BG == 0 {
		st.BG = cInk
	}
	if i, ok := r.styles[st]; ok {
		return i
	}
	fgc := -1
	if st.FG != 0 {
		fgc = int(st.FG & 0xFFFFFF)
	}
	r.styles[st] = len(r.out.Styles)
	r.out.Styles = append(r.out.Styles, [3]int{fgc, int(st.BG & 0xFFFFFF), int(st.Attr)})
	return r.styles[st]
}

// shoot renders the model and records the cells that changed, held for d.
func (r *recorder) shoot(d time.Duration) {
	cv := r.m.draw()
	if cv == nil {
		r.t.Fatal("tour frame too small")
	}
	ch := &r.out.Chapters[len(r.out.Chapters)-1]
	if r.out.Tabs == nil {
		for _, h := range r.m.hits {
			if h.y == 1 && strings.HasPrefix(h.key, "tab:") {
				r.out.Tabs = append(r.out.Tabs, [2]int{h.x, h.w})
			}
		}
	}
	ops := [][]any{}
	for y := 0; y < cv.H; y++ {
		row := cv.cells[y*cv.W : (y+1)*cv.W]
		same := func(x int) bool { return r.prev != nil && r.prev[y*cv.W+x] == row[x] }
		for x := 0; x < cv.W; {
			if same(x) {
				x++
				continue
			}
			// A run of changes, closing gaps of up to six unchanged cells
			// and never splitting a wide rune from its right half.
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
			var text strings.Builder
			runs := []int{}
			for i := start; i < end; i++ {
				p := row[i]
				switch {
				case p.cont:
					text.WriteRune(1)
				case utf8.RuneCountInString(p.ch) != 1:
					rn, _ := utf8.DecodeRuneInString(p.ch)
					text.WriteRune(rn)
				default:
					text.WriteString(p.ch)
				}
				s := r.style(p.st)
				if n := len(runs); n > 0 && runs[n-2] == s {
					runs[n-1]++
				} else {
					runs = append(runs, s, 1)
				}
			}
			ops = append(ops, []any{y, start, text.String(), runs})
			x = end
		}
	}
	ch.Frames = append(ch.Frames, tourFrame{D: int(d / time.Millisecond), Ops: ops})
	r.prev = append([]cell(nil), cv.cells...)
}

// play runs the model for total of simulated time, a frame every step,
// each shown for show (shorter than step for a time-lapse). Snapshots
// refresh every second, as in topo preview.
func (r *recorder) play(total, step, show time.Duration) {
	for el := time.Duration(0); el < total; el += step {
		r.shoot(show)
		r.wait(step)
	}
}

func (r *recorder) wait(d time.Duration) {
	r.m.Now = r.m.Now.Add(d)
	if r.m.Now.Sub(r.fetched) >= time.Second {
		r.fetch()
	}
}

// key presses k and shows the result for hold.
func (r *recorder) key(k string, hold time.Duration) {
	switch r.m.Key(k) {
	case "view":
		r.m.NextObserver()
		r.fetch()
	}
	r.shoot(hold)
	r.wait(hold)
}

func (r *recorder) observe(id string) {
	r.m.Observer = id
	r.m.Selected, r.m.Offset = 0, 0
	r.fetch()
}

const milli = time.Millisecond

// record is the tour: an outage arriving on the Overview, its route and
// evidence, the same network from two vantage points, the three maps, the
// servers, the incidents and the About globe.
func (r *recorder) record() {
	m := r.m
	s := time.Second

	r.chapter("outage")
	r.play(3*s, 500*milli, 500*milli)
	r.key("x", 900*milli)
	r.key("j", 1200*milli)
	m.modal = nil
	r.sim.SetScenario(1, m.Now)
	name, _ := scenarioName(r.sim.Scenarios()[1])
	m.say("Scenario: "+name+" — states change as new samples arrive", false)
	r.fetch()
	// Evidence arrives sample by sample: a 75 s time-lapse.
	r.play(75*s, s, 160*milli)
	r.play(2*s, 500*milli, 500*milli)

	r.chapter("route")
	for i := 0; i < 5; i++ {
		m.Selected = i
		r.play(1500*milli, 500*milli, 500*milli)
	}
	m.Selected = 0
	r.key("enter", 4*s)
	m.Detail = false

	r.chapter("vantage")
	r.observe("cs-mac")
	r.play(3*s, 500*milli, 500*milli)
	r.observe("obs-s1")
	r.play(3*s, 500*milli, 500*milli)
	r.observe("")

	m.setTab(tabTopology)
	m.MapMode = "virtual"
	r.chapter("virtual")
	r.play(5*s, 140*milli, 140*milli)

	m.MapMode = "geo"
	r.chapter("geo")
	r.play(5*s, 140*milli, 140*milli)

	m.MapMode = "network"
	r.chapter("network")
	r.play(5*s, 140*milli, 140*milli)
	m.MapMode = ""

	m.setTab(tabServers)
	r.chapter("servers")
	for i := 0; i < 4; i++ {
		m.Selected = i
		r.play(1500*milli, 500*milli, 500*milli)
	}

	m.setTab(tabIncidents)
	r.chapter("incidents")
	r.play(2*s, 500*milli, 500*milli)
	r.key("enter", 4*s)

	m.setTab(tabAbout)
	m.aboutAt = m.Now
	r.chapter("about")
	r.play(7*s, 100*milli, 100*milli)
}

func TestSite(t *testing.T) {
	dir := os.Getenv("TOPO_SITE")
	if dir == "" {
		t.Skip("set TOPO_SITE to the site's data directory (make site)")
	}
	// Timestamps in the evidence read the same wherever the tour is made.
	local := time.Local
	time.Local = time.UTC
	defer func() { time.Local = local }()
	for _, lang := range preview.Languages() {
		r := newRecorder(t, lang)
		r.record()
		b, e := json.Marshal(r.out)
		if e != nil {
			t.Fatal(e)
		}
		frames := 0
		for _, c := range r.out.Chapters {
			frames += len(c.Frames)
		}
		t.Logf("%s: %d chapters, %d frames, %d styles, %d bytes", lang, len(r.out.Chapters), frames, len(r.out.Styles), len(b))
		if e := os.WriteFile(filepath.Join(dir, "tour."+lang+".json"), b, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(dir, "land.bin"), landBits, 0o644); e != nil {
		t.Fatal(e)
	}
}
