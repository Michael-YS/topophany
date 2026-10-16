//go:build js && wasm

// topo-demo exposes the preview and read-only collector viewer to a browser.
package main

import (
	"encoding/json"
	"strconv"
	"syscall/js"
	"time"

	"github.com/WhitenWhiten/topophany/internal/tui"
)

type browserInput struct {
	Action        string   `json:"action"`
	Mode          string   `json:"mode"`
	Language      string   `json:"language"`
	Key           string   `json:"key"`
	Keys          []string `json:"keys"`
	Scenario      string   `json:"scenario"`
	Server        string   `json:"server"`
	Project       string   `json:"project"`
	Token         string   `json:"token"`
	Cols          int      `json:"cols"`
	Rows          int      `json:"rows"`
	Now           int64    `json:"now"`
	CellAspect    float64  `json:"cellAspect"`
	ReconnectHint string   `json:"reconnectHint"`
}

type browserSession interface {
	Key(string, time.Time)
	Frame(time.Time, int, int, bool) *tui.DemoFrame
	Close()
	Measured(time.Duration, int, time.Time)
}

type controller struct{ session browserSession }

func failure(message string) string {
	out, _ := json.Marshal(map[string]string{"error": message})
	return string(out)
}

func (c *controller) call(raw string) string {
	var input browserInput
	if json.Unmarshal([]byte(raw), &input) != nil {
		return failure("invalid viewer input")
	}
	now := time.Now()
	if previewMode && input.Now > 0 {
		now = time.UnixMilli(input.Now)
	}
	if input.Action == "init" || input.Action == "reset" {
		if c.session != nil {
			c.session.Close()
		}
		c.session = nil
		session, err := newBrowserSession(input, now)
		if err != nil {
			return failure(err.Error())
		}
		c.session = session
		if input.CellAspect >= 1 && input.CellAspect <= 4 {
			c.session.Key("cell:"+strconv.Itoa(int(input.CellAspect*1000))+":1000", now)
		}
	}
	if c.session == nil {
		return failure("viewer is not initialized")
	}
	if input.Action == "key" {
		c.session.Key(input.Key, now)
	}
	if len(input.Keys) > 16 {
		return failure("too many viewer inputs")
	}
	for _, key := range input.Keys {
		if len(key) > 32768 {
			return failure("viewer input too large")
		}
		c.session.Key(key, now)
	}
	if input.Action == "scenario" {
		if p, ok := c.session.(interface{ SetScenario(string, time.Time) }); ok {
			p.SetScenario(input.Scenario, now)
		}
	}
	start := time.Now()
	frame := c.session.Frame(now, input.Cols, input.Rows, input.Action != "tick")
	if frame == nil {
		return ""
	}
	out, err := json.Marshal(struct {
		Frame *tui.DemoFrame `json:"frame"`
	}{frame})
	if err != nil {
		return failure("cannot encode viewer")
	}
	c.session.Measured(time.Since(start), len(out), now)
	return string(out)
}

func main() {
	c := &controller{}
	call := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 {
			return failure("invalid viewer input")
		}
		raw := args[0].String()
		// A Go callback must return to JS before Fetch promises can resolve.
		// The worker serializes these Promise-returning calls.
		executor := js.FuncOf(func(_ js.Value, p []js.Value) any {
			resolve := p[0]
			go func() { resolve.Invoke(c.call(raw)) }()
			return nil
		})
		promise := js.Global().Get("Promise").New(executor)
		executor.Release()
		return promise
	})
	js.Global().Set("topophanyDemo", call)
	select {}
}
