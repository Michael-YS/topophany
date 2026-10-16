//go:build !nodevice

package app

import (
	"flag"
	"fmt"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/tui"
)

// deviceFlag adds --device: the TUI laid out for a phone's or a tablet's
// screen, simulated in the middle of the terminal. A build with -tags
// nodevice has no such flag (device_off.go).
func deviceFlag(f *flag.FlagSet) *string {
	ids := []string{}
	for _, d := range tui.Devices() {
		ids = append(ids, d.ID)
	}
	return f.String("device", "", "lay the TUI out for a device's screen and simulate it in the terminal: "+strings.Join(ids, ", ")+"; [ and ] fold through its poses, \\ picks the next device")
}

// withDevice checks --device and puts it in the TUI's startup.
func withDevice(s tui.Startup, device string) (tui.Startup, error) {
	if device == "" {
		return s, nil
	}
	if _, e := tui.DeviceByID(device); e != nil {
		return s, fmt.Errorf("--device: %w", e)
	}
	s.Device = device
	return s, nil
}
