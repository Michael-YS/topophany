package tui

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A host check's charts are drawn from the samples kept for it: its history,
// read once, then each newer sample a snapshot brings, each kept once, in
// order, cut down to the percentages and only under the newest contract.
func TestTrendsKeepNewestSamplesUnderOneContract(t *testing.T) {
	m := &Model{}
	at := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	sample := func(sec int, contract, cpu string) model.Observation {
		return model.Observation{Contract: contract, SampledAt: at.Add(time.Duration(sec) * time.Second), Outcome: "ok", Evidence: "reported",
			Facts: map[string]string{"cpu_used_percent": cpu, "memory_used_percent": "40", "disk_used_percent": "60", "process_name": "caddy", "os": "linux"}}
	}
	m.keepTrend("h", []model.Observation{sample(10, "a", "30"), sample(0, "a", "10"), sample(5, "a", "20")})
	m.keepTrend("h", []model.Observation{sample(5, "a", "99")})
	got := m.trendSamples("h")
	if len(got) != 3 || got[0].Facts["cpu_used_percent"] != "10" || got[1].Facts["cpu_used_percent"] != "20" || got[2].Facts["cpu_used_percent"] != "30" {
		t.Fatal("samples not kept once in order", got)
	}
	if len(got[0].Facts) != len(chartFacts) || got[0].Facts["process_name"] != "" {
		t.Fatal("a kept sample holds more than the charts draw", got[0].Facts)
	}
	m.keepTrend("h", []model.Observation{sample(15, "b", "50")})
	if got := m.trendSamples("h"); len(got) != 1 || got[0].Contract != "b" {
		t.Fatal("samples of an old contract were kept", got)
	}
	for i := range trendKeep + 10 {
		m.keepTrend("h", []model.Observation{sample(20+i, "b", "50")})
	}
	if got := m.trendSamples("h"); len(got) != trendKeep || !got[len(got)-1].SampledAt.Equal(at.Add(time.Duration(20+trendKeep+9)*time.Second)) {
		t.Fatal("not the newest samples kept", len(got))
	}
	if u := usageOf(model.Observation{Outcome: "error", Evidence: "reported", Facts: map[string]string{"cpu_used_percent": "5"}}); !math.IsNaN(u.cpu) {
		t.Fatal("an unread sample has a reading", u)
	}
	if u := usageOf(sample(0, "", "101")); !math.IsNaN(u.cpu) || u.memory != 40 {
		t.Fatal("an impossible percentage was read", u)
	}
}

func TestTrendsReadHistoryOnceAndTakeSnapshotSamples(t *testing.T) {
	m := previewModel(t)
	sim := m.Preview.(*preview.Sim)
	hosts := []string{}
	for _, x := range m.Snapshot.Topology.Checks {
		if x.Kind == "host" {
			hosts = append(hosts, x.ID)
		}
	}
	if len(hosts) != 11 || m.wantTrend() != hosts[0] {
		t.Fatal("the first host check's history is not wanted", m.wantTrend(), hosts)
	}
	m.KeepTrend(hosts[0], nil, errors.New("collector unreachable"))
	if m.wantTrend() != hosts[1] {
		t.Fatal("a failed history read is retried", m.wantTrend())
	}
	for id := m.wantTrend(); id != ""; id = m.wantTrend() {
		entries, e := sim.History(id, view.StripSamples, m.Now)
		m.KeepTrend(id, entries, e)
	}
	if n := len(m.trendSamples(hosts[1])); n != view.StripSamples {
		t.Fatal("history not kept", n)
	}
	// The snapshot's samples join the kept ones once per snapshot, and the
	// one whose history could not be read starts from them.
	m.draw()
	if n := len(m.trendSamples(hosts[0])); n != 1 {
		t.Fatal("the snapshot's sample was not taken", n)
	}
	m.Now = m.Now.Add(10 * time.Second)
	snap, e := sim.Snapshot("", m.Now)
	if e != nil {
		t.Fatal(e)
	}
	m.Snapshot = snap
	m.draw()
	m.draw()
	if n := len(m.trendSamples(hosts[0])); n != 2 {
		t.Fatal("a newer snapshot's sample was not taken once", n)
	}
	m.Edit = &Editor{}
	if m.wantTrend() != "" {
		t.Fatal("the editor reads history")
	}
}

// The sampler reads the selected check's samples first, then the history of
// each host check its charts have not read, one read at a time.
func TestSamplerReadsHostHistoryAfterTheSelectedCheck(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabChecks)
	selected := m.Rows()[0].ID
	read := []string{}
	s := &sampler{read: func(_ context.Context, check string, _ int) ([]model.Observation, error) {
		read = append(read, check)
		return nil, nil
	}, results: make(chan fetchedSamples, 1)}
	ctx := context.Background()
	s.request(ctx, m, false)
	got := <-s.results
	if got.check != selected || got.trend {
		t.Fatal("the selected check was not read first", got)
	}
	s.busy = false
	m.KeepSamples(got.check, got.entries, got.err)
	s.request(ctx, m, false)
	got = <-s.results
	if !got.trend || got.check != m.wantTrend() {
		t.Fatal("a host check's history was not read next", got)
	}
	if len(read) != 2 {
		t.Fatal(read)
	}
}

// The card's panels say each resource's reading now over a chart that fills
// as far as it is used; with no current reading they say none and keep only
// the faint past.
func TestServerCardResourcePanels(t *testing.T) {
	m := previewModel(t)
	m.setTab(tabServers)
	m.Selected = slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == "app" })
	m.draw() // which takes the snapshot's samples
	f := m.fleet()
	rs := f.Card("app").Resources
	rows := m.resourceRows(rs, 46)
	text := func(rows []cardRow) string {
		var b strings.Builder
		for _, r := range rows {
			for _, sp := range append(r.left, r.right...) {
				b.WriteString(sp.s)
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	out := text(rows)
	for _, want := range []string{"CPU", "MEM", "DISK", "8 CPUs", "GiB", "caddy ×1", "180 processes", "█"} {
		if !strings.Contains(out, want) {
			t.Fatalf("card resources lack %q:\n%s", want, out)
		}
	}
	for _, r := range rows[:4] {
		w := 0
		for _, sp := range r.left {
			w += width(sp.s)
		}
		if w != 46 || len(r.right) != 0 {
			t.Fatalf("a panel row is %d wide, not the card's 46: %q", w, text([]cardRow{r}))
		}
		if r.hit != "goto:check:"+rs[0].Check {
			t.Fatal("a resource row does not open its check", r.hit)
		}
	}
	if aside := m.resourceAside(rs); len(aside) != 1 || aside[0].s != "0s ago" {
		t.Fatal("the heading does not say how old the reading is", aside)
	}
	m.Error = "collector unavailable"
	rs = m.fleet().Card("app").Resources
	out = text(m.resourceRows(rs, 46))
	if strings.Contains(out, "%") || strings.Contains(out, "GiB") || strings.Contains(out, "processes") || strings.Count(out, "—") != 3 {
		t.Fatalf("a cached card shows readings:\n%s", out)
	}
	if aside := m.resourceAside(rs); !strings.HasPrefix(aside[0].s, "cached · read ") {
		t.Fatal("a cached card does not say so", aside)
	}
}

// A host check's evidence charts what it measured on the strip's time axis,
// with each limit and the processes, in place of the time it took.
func TestHostCheckEvidenceChartsResources(t *testing.T) {
	m := previewModel(t)
	sim := m.Preview.(*preview.Sim)
	sim.SetScenario(slices.Index(sim.ScenarioIDs(), "host-cpu-high"), m.Now.Add(-time.Minute))
	snap, e := sim.Snapshot("", m.Now)
	if e != nil {
		t.Fatal(e)
	}
	m.Snapshot = snap
	m.setTab(tabChecks)
	m.Selected = slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == "host-app" })
	m.Detail = true
	entries, e := sim.History("host-app", view.StripSamples, m.Now)
	if e != nil {
		t.Fatal(e)
	}
	m.KeepSamples("host-app", entries, nil)
	out := visible(m)
	for _, want := range []string{"RECENT SAMPLES", "CPU", "MEMORY", "DISK", "90% limit", "8 CPUs", "now", "PROCESSES", "caddy ×1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("host evidence lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "p50") {
		t.Fatalf("host evidence charts how long sampling took:\n%s", out)
	}
	if !strings.ContainsFunc(out, func(r rune) bool { return r > 0x2800 && r <= 0x28FF }) {
		t.Fatal("no chart drawn")
	}
}

// A node's evidence draws its local resources as charts in place of their
// lines.
func TestNodeEvidenceChartsLocalResources(t *testing.T) {
	m := previewModel(t)
	m.Height = 80
	m.setTab(tabTopology)
	m.Selected = slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == "app" })
	m.Detail = true
	out := visible(m)
	for _, want := range []string{"LOCAL RESOURCES", "MEMORY", "PROCESSES", "Access endpoints", "app-web"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Fatalf("node evidence lacks %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"Local resources:", "Resource check", "Sampled"} {
		if strings.Contains(out, gone) {
			t.Fatalf("node evidence still lists %q:\n%s", gone, out)
		}
	}
}

func TestResourceCharts(t *testing.T) {
	nan := math.NaN()
	rows := tank([]float64{100, 50, 25, nan}, 2, 90, true, cInk)
	cells := func(l vline) []rune {
		out := []rune{}
		for _, sp := range l {
			out = append(out, []rune(sp.s)...)
		}
		return out
	}
	if got := string(cells(rows[0])) + "|" + string(cells(rows[1])); got != "█   |██▄ " {
		t.Fatalf("tank %q", got)
	}
	if rows[0][0].st.FG == rows[1][1].st.FG {
		t.Fatal("a reading past its limit is not colored apart")
	}
	if loadColor(95, 90) != cWarn || loadColor(10, 90) != cAccent || loadColor(70, 0) == cWarn {
		t.Fatal("readings colored against the wrong limit")
	}
	area := areaChart([]float64{10, nan, 10}, 2, 75, true)
	top, foot := cells(area[0]), cells(area[1])
	// 75% of 8 dots is the top row's second dot: drawn where nothing is.
	if top[0] != 0x2800+0x04 || top[1] != 0x2800+0x04 || foot[1] != ' ' || foot[0] == ' ' {
		t.Fatalf("area %q %q", string(top), string(foot))
	}
	if got := padNaN([]float64{1, 2}, 4); !math.IsNaN(got[0]) || got[3] != 2 || len(padNaN([]float64{1, 2, 3}, 2)) != 2 {
		t.Fatal("pad", got)
	}
	for _, c := range []struct {
		used, total uint64
		want        string
	}{{7 << 30 / 2, 16 << 30, "3.5/16 GiB"}, {150 << 30, 256 << 30, "150/256 GiB"}, {1 << 40, 2 << 40, "1.0/2.0 TiB"}, {500 << 20, 1000 << 20, "0.5/1.0 GiB"}} {
		if got := capacity(c.used, c.total); got != c.want {
			t.Errorf("capacity(%d, %d) = %q, want %q", c.used, c.total, got, c.want)
		}
	}
}
