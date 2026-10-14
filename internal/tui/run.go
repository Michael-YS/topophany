package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type Fetch func(context.Context, string) (model.Snapshot, string, error)
type fetched struct {
	view     string
	snapshot model.Snapshot
	server   string
	err      error
	took     time.Duration
}

func Run(ctx context.Context, c *client.Client, observer string, noColor bool, in, out *os.File) error {
	return RunWithFetch(ctx, c.Snapshot, observer, noColor, in, out)
}
func RunWithFetch(parent context.Context, fetch Fetch, observer string, noColor bool, in, out *os.File) error {
	return loop(parent, &Model{Observer: observer, NoColor: noColor}, fetch, in, out)
}

// RunEditor opens the editor on a local topology. It never contacts a
// collector; the file changes only on an explicit save.
func RunEditor(parent context.Context, path string, c model.Config, noColor bool, in, out *os.File) error {
	m := &Model{NoColor: noColor, Edit: &Editor{Path: path, Config: c}, Now: time.Now()}
	m.rebuild()
	if len(c.Nodes) == 0 {
		m.setTab(tabTopology)
		m.say("New topology — press a to add your first node", false)
	}
	return loop(parent, m, nil, in, out)
}

// ColorProfile picks truecolor where the terminal advertises it, otherwise
// the xterm 256-colour cube.
func ColorProfile() int {
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	if ct == "truecolor" || ct == "24bit" || os.Getenv("WT_SESSION") != "" {
		return colorTrue
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty", "Hyper", "Tabby":
		return colorTrue
	}
	if strings.Contains(os.Getenv("TERM"), "direct") || strings.HasPrefix(os.Getenv("TERM"), "xterm-kitty") {
		return colorTrue
	}
	return color256
}

// screen repaints only the cells that changed since the previous frame,
// inside a synchronized-update block so animation does not tear.
type screen struct {
	w, h  int
	prev  *Canvas
	small string // the line shown while the terminal is too small
}

// frame is what moves the terminal from the previous frame to cv, or to
// the one-line notice when cv is nil; "" when nothing changed.
func (s *screen) frame(cv *Canvas, notice string, profile, w, h int) string {
	var b strings.Builder
	if w != s.w || h != s.h {
		b.WriteString("\x1b[0m\x1b[2J")
		s.prev, s.small = nil, ""
		s.w, s.h = w, h
	}
	diff := ""
	switch {
	case cv == nil:
		if s.prev != nil || notice != s.small {
			diff = "\x1b[H" + notice + "\x1b[J"
		}
		s.prev, s.small = nil, notice
	default:
		diff = cv.Diff(s.prev, profile)
		s.prev, s.small = cv, ""
	}
	if diff != "" {
		b.WriteString("\x1b[?2026h" + diff + "\x1b[?2026l")
	}
	return b.String()
}

func (s *screen) paint(out *os.File, m *Model) (int, error) {
	cv := m.draw()
	notice := ""
	if cv == nil {
		notice = m.Render()[0]
	}
	f := s.frame(cv, notice, m.profile(), m.Width, m.Height)
	if f == "" {
		return 0, nil
	}
	return fmt.Fprint(out, f)
}

func loop(parent context.Context, m *Model, fetch Fetch, in, out *os.File) error {
	term, e := openTerminal(in, out)
	if e != nil {
		return e
	}
	defer term.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// Alternate screen, hidden cursor, bracketed paste, button + SGR mouse.
	if _, e = fmt.Fprint(out, "\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[?1000h\x1b[?1006h\x1b[2J"); e != nil {
		return e
	}
	defer fmt.Fprint(out, "\x1b[0m\x1b[?1006l\x1b[?1000l\x1b[?2004l\x1b[?25h\x1b[?1049l")
	if !m.NoColor {
		m.Colors = ColorProfile()
	}
	m.Transparent = !paintBackground()
	m.Now = time.Now()
	m.Width, m.Height = term.Size()
	ch := make(chan fetched, 1)
	busy := false
	request := func() {
		if busy || fetch == nil {
			return
		}
		busy = true
		v := m.Observer
		go func() {
			began := time.Now()
			snap, server, e := fetch(ctx, v)
			select {
			case ch <- fetched{v, snap, server, e, time.Since(began)}:
			case <-ctx.Done():
			}
		}()
	}
	m.Loading = "connecting to the collector"
	request()
	keys := time.NewTicker(30 * time.Millisecond)
	defer keys.Stop()
	every := 3 * time.Second
	if m.Preview != nil {
		every = time.Second
	}
	refresh := time.NewTicker(every)
	defer refresh.Stop()
	decoder := Decoder{}
	scr := &screen{}
	last := time.Time{}
	draw := func() error {
		m.Now = time.Now()
		m.Width, m.Height = term.Size()
		last = m.Now
		n, e := scr.paint(out, m)
		m.measured(time.Since(last), n, last)
		return e
	}
	if e = draw(); e != nil {
		return e
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-ch:
			busy = false
			m.perf.fetch, m.perf.fetches = msg.took, m.perf.fetches+1
			if msg.view != m.Observer {
				request()
				continue
			}
			m.Loading = ""
			if msg.err != nil {
				m.Error = msg.err.Error()
			} else {
				m.Snapshot = msg.snapshot
				m.Server = msg.server
				m.Error = ""
			}
			if e = draw(); e != nil {
				return e
			}
		case <-refresh.C:
			request()
		case now := <-keys.C:
			b, e := term.Read()
			if e != nil {
				return e
			}
			ks := decoder.Feed(b, now)
			for _, k := range ks {
				m.Now = now
				switch m.Key(k) {
				case "quit":
					return nil
				case "refresh":
					request()
				case "view":
					m.NextObserver()
					topology := m.Snapshot.Topology
					m.Snapshot = model.Snapshot{Topology: topology}
					m.Error = ""
					m.Loading = "loading the view from " + m.Observer
					if m.Observer == "" {
						m.Loading = "loading all observers"
					}
					request()
				}
			}
			if len(ks) > 0 || now.Sub(last) >= m.Animating() {
				if e = draw(); e != nil {
					return e
				}
			}
		}
	}
}

// ReadSecret uses the same nonblocking console backend but never echoes the
// token. Prefer --token-stdin in automation; command-line token flags are only
// an explicit escape hatch and may be visible in a local process listing.
func ReadSecret(ctx context.Context, in, out *os.File) (string, error) {
	term, e := openTerminal(in, out)
	if e != nil {
		return "", e
	}
	defer term.Close()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	decoder := Decoder{}
	runes := []rune{}
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case now := <-tick.C:
			b, e := term.Read()
			if e != nil {
				return "", e
			}
			for _, k := range decoder.Feed(b, now) {
				switch k {
				case "ctrl-c", "esc":
					return "", context.Canceled
				case "enter":
					fmt.Fprint(out, "\r\n")
					return string(runes), nil
				case "backspace":
					if len(runes) > 0 {
						runes = runes[:len(runes)-1]
					}
				default:
					if strings.HasPrefix(k, "paste:") {
						runes = append(runes, []rune(strings.TrimPrefix(k, "paste:"))...)
					} else if len([]rune(k)) == 1 {
						runes = append(runes, []rune(k)[0])
					}
				}
				if len(runes) > 512 {
					return "", errors.New("token too long")
				}
			}
		}
	}
}
