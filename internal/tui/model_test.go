package tui

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fixtureModel() Model {
	c := testkit.Config()
	now := time.Now().UTC()
	var rows []model.Observation
	for i := 0; i < 3; i++ {
		rows = append(rows, testkit.Observation(c, "home-http", uint64(i+1), now.Add(-time.Duration(3-i)*time.Second), "fail"))
	}
	rows = append(rows, testkit.Observation(c, "cloud-http", 4, now.Add(-time.Second), "ok"))
	snap := engine.Build(c, "primary", "", rows, nil, now)
	return Model{Snapshot: snap, Now: now, Width: 120, Height: 30, NoColor: true}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func TestEveryTabFrameBoundsAndDetails(t *testing.T) {
	for _, dim := range [][2]int{{30, 10}, {79, 24}, {100, 28}, {160, 45}} {
		for tab := 0; tab < len(tabs); tab++ {
			m := fixtureModel()
			m.Width, m.Height = dim[0], dim[1]
			m.Tab = tab
			for _, detail := range []bool{false, true} {
				m.Detail = detail
				f := ansi.ReplaceAllString(m.Frame(), "")
				ls := strings.Split(f, "\r\n")
				if len(ls) != dim[1] {
					t.Fatal(tab, dim, len(ls))
				}
				for _, l := range ls {
					if width(l) != dim[0] {
						t.Fatalf("tab %d dims %v width %d line %q", tab, dim, width(l), l)
					}
				}
			}
		}
	}
}
func TestCachedViewDoesNotShowGreen(t *testing.T) {
	for _, kind := range []string{"error", "expired", "never"} {
		for tab := 0; tab < len(tabs); tab++ {
			m := fixtureModel()
			m.Tab = tab
			switch kind {
			case "error":
				m.Error = "collector unavailable"
			case "expired":
				m.Now = m.Now.Add(time.Minute)
			case "never":
				m.Snapshot.GeneratedAt = time.Time{}
			}
			if !m.Cached() {
				t.Fatal("view still current")
			}
			for _, r := range m.Rows() {
				if r.Health != "unknown" || !strings.Contains(strings.Join(r.Details, " "), "CACHED") {
					t.Fatal("cached item green", r)
				}
			}
			if strings.Contains(m.Frame(), "● UP") {
				t.Fatal("green cached frame")
			}
		}
	}
}
func TestKeyboardNavigationSearchAndEmptyViews(t *testing.T) {
	m := fixtureModel()
	for i := 0; i < len(tabs); i++ {
		m.Key(string(rune('1' + i)))
		if m.Tab != i {
			t.Fatal(i)
		}
	}
	for _, k := range []string{"tab", "left", "right", "h", "l", "G", "g", "down", "up", "j", "k", "pgdown", "pgup", "enter"} {
		m.Key(k)
		m.Frame()
	}
	if m.Key("v") != "view" || m.Key("r") != "refresh" || m.Key("q") != "quit" {
		t.Fatal("actions missing")
	}
	m.Key("/")
	m.Key("住")
	m.Key("宅")
	if m.Search != "住宅" {
		t.Fatal(m.Search)
	}
	m.Key("backspace")
	if m.Search != "住" {
		t.Fatal(m.Search)
	}
	m.Key("paste:" + strings.Repeat("q", 200) + "\x1b[2J")
	if len([]rune(m.Search)) != 120 {
		t.Fatal("search budget")
	}
	m.Key("enter")
	m.Key("esc")
	if m.Search != "" {
		t.Fatal("filter not cleared")
	}
	m.Key("/")
	if m.Key("ctrl-c") != "quit" {
		t.Fatal("search quit missing")
	}
	m = Model{Width: 80, Height: 24, NoColor: true}
	for i := 0; i < len(tabs); i++ {
		m.Tab = i
		m.Key("G")
		m.Key("j")
		m.Detail = true
		m.Key("pgdown")
		m.Frame()
		if m.Selected < 0 || m.Offset < 0 {
			t.Fatal(m)
		}
	}
}
func TestTerminalEscapesAndWideText(t *testing.T) {
	m := fixtureModel()
	m.Tab = 1
	m.Snapshot.Topology.Nodes[0].Name = "家里\x1b[2J\x1b]0;fake\a\u202e\u061c\u200e\u200fhost"
	frame := m.Frame()
	if strings.Contains(frame, "\x1b[2J") || strings.Contains(frame, "\x1b]0;") || strings.ContainsAny(frame, "\u202e\u061c\u200e\u200f") {
		t.Fatal("terminal injection")
	}
	if width("住宅") != 4 || width("e\u0301") != 1 {
		t.Fatal("unicode width")
	}
	for _, w := range []int{0, 1, 2, 3, 10} {
		s := fit("住e\u0301宅", w)
		if width(s) != w || !utf8.ValidString(s) {
			t.Fatal(w, s, width(s))
		}
	}
	for _, l := range wrap("住宅服务归属", 4) {
		if width(l) > 4 {
			t.Fatal(l)
		}
	}
	m.Width = 20
	m.Height = 8
	if !strings.Contains(m.Frame(), "Topophany") {
		t.Fatal("small terminal message")
	}
	m.NoColor = false
	m.Width = 120
	m.Height = 30
	if !strings.Contains(m.Frame(), "\x1b[0;1;38;2;") {
		t.Fatal("color rendering")
	}
}
func TestPathCandidatesAndNotesAreNotFabricatedHops(t *testing.T) {
	m := fixtureModel()
	m.Tab = tabPaths
	found := false
	for _, r := range m.Rows() {
		if r.ID == "residential" {
			found = true
			if !strings.Contains(strings.Join(r.Details, " "), "NOT serial hops") {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal("path missing")
	}
	m.Tab = tabServices
	for _, r := range m.Rows() {
		if r.ID == "notes" && r.Health != "unknown" {
			t.Fatal("notes service marked up")
		}
	}
	if age(m.Now, time.Time{}) != "never" || age(m.Now, m.Now.Add(time.Second)) != "clock ahead" {
		t.Fatal("age edge cases")
	}
}
func TestDecoderFragmentedKeysAndPaste(t *testing.T) {
	var d Decoder
	now := time.Now()
	if len(d.Feed([]byte("\x1b["), now)) != 0 {
		t.Fatal("partial escape emitted")
	}
	ks := d.Feed([]byte("A"), now.Add(time.Millisecond))
	if len(ks) != 1 || ks[0] != "up" {
		t.Fatal(ks)
	}
	b := []byte("住宅")
	if len(d.Feed(b[:2], now)) != 0 {
		t.Fatal("partial UTF8")
	}
	ks = d.Feed(b[2:], now)
	if strings.Join(ks, "") != "住宅" {
		t.Fatal(ks)
	}
	ks = d.Feed([]byte("\x1b[200~qrv\x1b[201~"), now)
	if len(ks) != 1 || ks[0] != "paste:qrv" {
		t.Fatal(ks)
	}
	m := fixtureModel()
	if m.Key(ks[0]) != "" {
		t.Fatal("paste executed navigation")
	}
	d.Feed([]byte{27}, now)
	if ks = d.Feed(nil, now.Add(time.Second)); len(ks) != 1 || ks[0] != "esc" {
		t.Fatal(ks)
	}
	ks = d.Feed([]byte{3, 9, 13, 127, 0, 1}, now)
	if strings.Join(ks, ",") != "ctrl-c,tab,enter,backspace" {
		t.Fatal(ks)
	}
}
func TestNonTerminalFailsWithoutControlOutput(t *testing.T) {
	in, e := os.Open(os.DevNull)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	out, e := os.CreateTemp(t.TempDir(), "not-tty")
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	e = RunWithFetch(context.Background(), func(context.Context, string) (model.Snapshot, string, error) {
		t.Error("fetch before terminal validation")
		return model.Snapshot{}, "", nil
	}, "", true, in, out)
	if e == nil {
		t.Fatal("redirected TUI accepted")
	}
	if _, e = ReadSecret(context.Background(), in, out); e == nil {
		t.Fatal("noninteractive secret prompt")
	}
	st, _ := out.Stat()
	if st.Size() != 0 {
		t.Fatal("control sequences sent to regular file")
	}
}
func FuzzDecoderAndRendering(f *testing.F) {
	f.Add([]byte("\x1b[A中文"), uint8(80))
	f.Add([]byte("\x1b[200~q\x1b[201~"), uint8(120))
	f.Fuzz(func(t *testing.T, b []byte, w uint8) {
		if len(b) > 16384 {
			return
		}
		var d Decoder
		now := time.Unix(1, 0)
		var m Model
		m.Width = int(w)
		m.Height = 15
		m.Now = now
		m.NoColor = true
		for start := 0; start < len(b); start += 7 {
			for _, k := range d.Feed(b[start:min(start+7, len(b))], now) {
				m.Key(k)
			}
			now = now.Add(time.Millisecond)
		}
		d.Feed(nil, now.Add(time.Second))
		frame := m.Frame()
		if !utf8.ValidString(frame) {
			t.Fatal("invalid UTF8 frame")
		}
	})
}

func TestOversizedFragmentedPasteNeverExecutesCommands(t *testing.T) {
	var d Decoder
	now := time.Now()
	if k := d.Feed([]byte("\x1b[200~"), now); len(k) != 0 {
		t.Fatal(k)
	}
	for i := 0; i < 100; i++ {
		if k := d.Feed([]byte(strings.Repeat("qrv", 100)), now); len(k) != 0 {
			t.Fatal("paste escaped into commands", k)
		}
		if len(d.buffer) > 5 || len(d.paste) > 8192 {
			t.Fatal("unbounded paste buffer")
		}
	}
	if k := d.Feed([]byte("\x1b[20"), now); len(k) != 0 {
		t.Fatal(k)
	}
	k := d.Feed([]byte("1~"), now)
	if len(k) != 1 || !strings.HasPrefix(k[0], "paste:") || len(k[0]) > 8198 {
		t.Fatal("bad truncated paste delivery")
	}
	if k = d.Feed([]byte("q"), now); len(k) != 1 || k[0] != "q" {
		t.Fatal("parser failed to resume after paste")
	}
}

// While something loads the UI says so with a spinner that turns, and
// repaints often enough for it to turn.
func TestLoadingSpins(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	m := &Model{Now: now, Width: 120, Height: 36, Server: "https://monitor.example.invalid:7447", Loading: "connecting to the collector"}
	a := visible(m)
	if !strings.Contains(a, "CONNECTING") || !strings.Contains(a, "connecting to the collector…") || !strings.Contains(a, "Waiting for the topology") {
		t.Fatal("loading frame does not say what it waits for", a)
	}
	m.Now = now.Add(90 * time.Millisecond)
	if visible(m) == a {
		t.Fatal("spinner does not turn")
	}
	if m.Animating() > 100*time.Millisecond {
		t.Fatal("loading frames are not repainted often enough to spin")
	}
	m.Loading = ""
	m.Snapshot.GeneratedAt = now
	if m.Animating() < 100*time.Millisecond {
		t.Fatal("still spinning after loading")
	}
}

// The run loop repaints as often as what is on screen moves: the glints on
// either map, the About intro and then its globe, and otherwise seldom.
func TestRepaintsArePacedToWhatMoves(t *testing.T) {
	m := previewModel(t)
	for _, c := range []struct {
		tab, w, h int
		after     time.Duration
		want      time.Duration
	}{
		{tabOverview, 160, 48, 0, 140 * time.Millisecond},
		{tabOverview, 80, 24, 0, 500 * time.Millisecond}, // no map
		{tabTopology, 80, 24, 0, 140 * time.Millisecond},
		{tabServices, 160, 48, 0, 500 * time.Millisecond},
		{tabAbout, 160, 48, time.Second, 70 * time.Millisecond},
		{tabAbout, 160, 48, 5 * time.Second, 100 * time.Millisecond},
	} {
		m.Width, m.Height = c.w, c.h
		m.setTab(c.tab)
		m.Now = m.Now.Add(c.after)
		if got := m.Animating(); got != c.want {
			t.Errorf("%s at %dx%d after %v repaints every %v, want %v", tabs[c.tab], c.w, c.h, c.after, got, c.want)
		}
	}
}
