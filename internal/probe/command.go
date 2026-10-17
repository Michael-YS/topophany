package probe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"os/exec"
	"strings"
	"time"
)

type cappedOutput struct {
	b     bytes.Buffer
	limit int
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	if w.b.Len()+len(p) > w.limit {
		return 0, errors.New("command output budget exceeded")
	}
	return w.b.Write(p)
}
func defaultCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedCommand(name, args) {
		return nil, errors.New("command not allowed")
	}
	p, e := exec.LookPath(name)
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, p, args...)
	cmd.WaitDelay = time.Second
	return runCaptured(cmd)
}

// runCaptured runs cmd with a bounded stdout and returns what it wrote even
// when it exits non-zero. A command can say something and still fail:
// tailscale ping reports an internal timeout on stdout and exits 1. The
// adapter decides whether that output is a failure or an answer.
func runCaptured(cmd *exec.Cmd) ([]byte, error) {
	w := &cappedOutput{limit: 2 << 20}
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	e := cmd.Run()
	return w.b.Bytes(), e
}

func allowedCommand(name string, args []string) bool {
	if name == "tailscale" {
		if len(args) == 2 && args[0] == "status" && args[1] == "--json" {
			return true
		}
		if len(args) != 5 && len(args) != 6 {
			return false
		}
		if args[0] != "ping" || args[1] != "--c=1" || args[2] != "--until-direct=false" || args[3] != "--timeout=2s" {
			return false
		}
		if len(args) == 6 && args[4] != "--tsmp" {
			return false
		}
		target, err := netip.ParseAddr(args[len(args)-1])
		return err == nil && target.Zone() == "" && target.IsGlobalUnicast() && !target.IsLoopback()
	}
	if name != "wg" || len(args) != 3 || args[0] != "show" || args[1] == "" || strings.HasPrefix(args[1], "-") || args[1] == "all" || args[1] == "interfaces" {
		return false
	}
	switch args[2] {
	case "latest-handshakes", "transfer", "allowed-ips", "persistent-keepalive":
		return true
	default:
		return false
	}
}
func (r Runner) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Command != nil {
		return r.Command(ctx, name, args...)
	}
	return defaultCommand(ctx, name, args...)
}
