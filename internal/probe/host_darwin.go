package probe

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func (r Runner) sampleHost(ctx context.Context, h model.HostOptions) (HostSample, error) {
	return sampleDarwinHost(ctx, h, darwinHostCommand, sampleDisk)
}

// Fixed system paths and argument lists; no shell, PATH lookup or commands
// supplied by a collector. Keep diagnostics free of command output.
func darwinHostCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.WaitDelay = time.Second
	w := &cappedOutput{limit: 8 << 20}
	cmd.Stdout, cmd.Stderr = w, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, hostReadError("command")
	}
	return w.b.Bytes(), nil
}
