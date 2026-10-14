package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// homelabModel renders examples/homelab.json with synthetic evidence; it is
// shared by the preview dump and the rendering tests.
func homelabModel(t testing.TB) Model {
	t.Helper()
	c, e := config.Load(filepath.Join("..", "..", "examples", "homelab.json"))
	if e != nil {
		t.Fatal(e)
	}
	for i := range c.Checks {
		c.Checks[i].Disabled = false
	}
	now := time.Now().UTC()
	rows := []model.Observation{}
	seq := uint64(1)
	for i, x := range c.Checks {
		outcome := "ok"
		if i%5 == 1 {
			outcome = "fail"
		}
		for k := 0; k < 3; k++ {
			rows = append(rows, testkit.Observation(c, x.ID, seq, now.Add(-time.Duration(3-k)*time.Second), outcome))
			seq++
		}
	}
	snap := engine.Build(c, "primary", "", rows, nil, now)
	return Model{Snapshot: snap, Now: now, Width: 140, Height: 40, Server: "https://monitor.example.invalid:7447"}
}

func TestPreviewDump(t *testing.T) {
	dir := os.Getenv("TOPO_PREVIEW")
	if dir == "" {
		t.Skip("set TOPO_PREVIEW=dir to dump rendered frames")
	}
	for _, color := range []bool{false, true} {
		for tab := range tabs {
			for _, mode := range []string{"virtual", "geo", "network"} {
				if tab != tabTopology && mode == "network" {
					continue
				}
				m := homelabModel(t)
				m.NoColor = !color
				m.setTab(tab)
				m.MapMode = mode
				m.aboutAt = m.Now.Add(-3 * time.Second)
				if tab == tabTopology {
					m.Selected = 3
				}
				name := tabs[tab] + "-" + mode
				if color {
					name += ".ansi"
				} else {
					name += ".txt"
				}
				os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(m.Render(), "\n")+"\x1b[0m\n"), 0644)
			}
		}
	}
}
