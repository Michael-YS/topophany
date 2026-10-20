//go:build nodevice

package tui

import "errors"

// Built with -tags nodevice, the TUI knows no devices: their layouts,
// the touch controls and the simulator are left out (device.go), and the
// terminal draws exactly as it does with them. What the rest of the TUI
// asks of them stands here, never reached, as no device can be set.

// Device is a device's screen; this build has none.
type Device struct{ ID, Model, Pose string }

// touch is what the touch layouts keep between frames; there are none.
type touch struct{ mapRect rect }

// Devices lists the screens the TUI is laid out for: none in this build.
func Devices() []Device { return nil }

// DeviceByID refuses every device in this build.
func DeviceByID(id string) (*Device, error) {
	return nil, errors.New("this topo is built without device simulation (-tags nodevice)")
}

func (m *Model) simulate() *Canvas                         { return nil }
func (m *Model) compactLayout() bool                       { return false }
func (m *Model) deviceMouse(string) string                 { return "" }
func (m *Model) simKey(string) (string, bool)              { return "", false }
func (m *Model) touchKey(string) string                    { return "" }
func (m *Model) mapIn(cv *Canvas, x, y, w, h int, sel Row) { m.drawMap(cv, x, y, w, h, sel) }
