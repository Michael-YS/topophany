package probe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
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
	if name != "tailscale" && name != "wg" {
		return nil, errors.New("command not allowed")
	}
	p, e := exec.LookPath(name)
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, p, args...)
	cmd.WaitDelay = time.Second
	w := &cappedOutput{limit: 2 << 20}
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		return nil, e
	}
	return w.b.Bytes(), nil
}
func (r Runner) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Command != nil {
		return r.Command(ctx, name, args...)
	}
	return defaultCommand(ctx, name, args...)
}
