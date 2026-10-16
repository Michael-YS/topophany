package tui

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// Preview is the scenario control of a synthetic evidence source
// (topo preview). Scenarios are "name\tdetail".
type Preview interface {
	Scenarios() []string
	Scenario() int
	SetScenario(i int, at time.Time)
}

// RunPreview runs the ordinary viewer against a synthetic source. fetch and
// history must never touch the network.
func RunPreview(parent context.Context, p Preview, fetch Fetch, history History, observer string, noColor bool, in, out *os.File, options ...Startup) error {
	m := &Model{Observer: observer, NoColor: noColor, Preview: p}
	m.initializeStartup(options)
	m.say("Preview: synthetic evidence only · x switch scenario · D debug overlay", false)
	return loop(parent, m, fetch, history, in, out)
}

const scenarioKeys = "1234567890abcdefghilnoprstuvwy"

func scenarioName(s string) (string, string) {
	name, detail, _ := strings.Cut(s, "\t")
	return name, detail
}

func (m *Model) scenarioMenu() {
	list := m.Preview.Scenarios()
	items := make([]menuItem, len(list))
	for i, s := range list {
		name, detail := scenarioName(s)
		key := ""
		if i < len(scenarioKeys) {
			key = scenarioKeys[i : i+1]
		}
		items[i] = menuItem{key: key, label: name, hint: detail}
	}
	cur := m.Preview.Scenario()
	m.modal = &modal{kind: "menu", title: "Preview scenario", items: items, sel: cur, pick: func(i int) string {
		m.Preview.SetScenario(i, time.Now())
		name, _ := scenarioName(list[i])
		m.say("Scenario: "+name+" — states change as new samples arrive", false)
		return "refresh"
	}}
}

func (m *Model) scenario() string {
	if m.Preview == nil {
		return ""
	}
	list := m.Preview.Scenarios()
	if i := m.Preview.Scenario(); i >= 0 && i < len(list) {
		name, _ := scenarioName(list[i])
		return name
	}
	return ""
}

// perf is what the debug overlay reports about the run loop.
type perf struct {
	render, avg, worst time.Duration
	bytes, peak        int
	frames             []time.Time
	fetch              time.Duration
	fetches            int
}

func (m *Model) measured(render time.Duration, bytes int, at time.Time) {
	p := &m.perf
	p.render = render
	if p.avg == 0 {
		p.avg = render
	}
	p.avg = (p.avg*7 + render) / 8
	p.worst = max(p.worst, render)
	p.bytes = bytes
	p.peak = max(p.peak, bytes)
	p.frames = append(p.frames, at)
	for len(p.frames) > 0 && at.Sub(p.frames[0]) > time.Second {
		p.frames = p.frames[1:]
	}
}

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64) + "ms"
}

func (m *Model) drawDebug(cv *Canvas) {
	p := m.perf
	nodes, edges, _ := m.graph()
	profile := map[int]string{colorTrue: "truecolor", color256: "xterm-256", colorNone: "NO_COLOR"}[m.profile()]
	lines := [][2]string{
		{"frame", ms(p.render) + "  avg " + ms(p.avg) + "  max " + ms(p.worst)},
		{"paint", itoa(p.bytes) + " B  peak " + itoa(p.peak) + " B  " + itoa(len(p.frames)) + " fps"},
		{"tick", ms(m.Animating()) + "  fetch " + ms(p.fetch) + " ×" + itoa(p.fetches)},
		{"screen", itoa(cv.W) + "×" + itoa(cv.H) + "  " + profile},
		{"view", "rows " + itoa(len(m.Rows())) + "  hits " + itoa(len(m.hits)) + "  sel " + itoa(m.Selected)},
		{"map", m.mapMode() + " z" + itoa(m.Zoom) + "  nodes " + itoa(len(nodes)) + "  edges " + itoa(len(edges))},
		{"data", itoa(len(m.Snapshot.Checks)) + " checks  age " + age(m.Now, m.Snapshot.GeneratedAt)},
	}
	if s := m.scenario(); s != "" {
		lines = append(lines, [2]string{"scenario", s})
	}
	w := min(46, cv.W-2)
	h := len(lines) + 2
	x, y := cv.W-w-1, cv.H-1-h
	if y < 3 || w < 24 {
		return
	}
	bg := Style{BG: cDeep}
	cv.Fill(x, y, w, h, bg)
	cv.Box(x, y, w, h, fg(cEdit).Bg(cDeep))
	cv.Text(x+2, y, " DEBUG ", fg(cDeep).Bg(cEdit).bold(), -1)
	cv.Text(x+w-11, y, " D close ", fg(cMuted).Bg(cDeep), -1)
	for i, l := range lines {
		cv.Text(x+2, y+1+i, l[0], bg.Fg(cMuted), 9)
		cv.Text(x+11, y+1+i, l[1], bg.Fg(cText), w-13)
	}
}
