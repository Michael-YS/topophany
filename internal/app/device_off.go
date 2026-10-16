//go:build nodevice

package app

import (
	"flag"

	"github.com/WhitenWhiten/topophany/internal/tui"
)

// Built with -tags nodevice, topo lays the TUI out for no device and has
// no --device flag.
func deviceFlag(*flag.FlagSet) *string { return new(string) }

func withDevice(s tui.Startup, _ string) (tui.Startup, error) { return s, nil }
