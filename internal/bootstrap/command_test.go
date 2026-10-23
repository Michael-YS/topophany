package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBootstrapCommandHelper(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "--bootstrap-helper-wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "--bootstrap-helper-fail":
		fmt.Fprintln(os.Stderr, "synthetic private command output")
		os.Exit(7)
	case "--bootstrap-helper-ok":
		os.Exit(0)
	}
}

func bootstrapCommand(t *testing.T, mode string) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe, []string{"-test.run=^TestBootstrapCommandHelper$", "--", "--bootstrap-helper-" + mode}
}

func TestCommandReportsParentCancellation(t *testing.T) {
	exe, args := bootstrapCommand(t, "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := run(ctx, exe, args...)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("command lost caller deadline", err)
	}
}

func TestCommandKeepsExitStatusWithoutPrivateOutput(t *testing.T) {
	exe, args := bootstrapCommand(t, "fail")
	err := run(context.Background(), exe, args...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || strings.Contains(err.Error(), "synthetic private command output") {
		t.Fatal("command lost exit status or exposed output", err)
	}
}

func TestCommandOperationBudgetAndSuccessfulExit(t *testing.T) {
	exe, args := bootstrapCommand(t, "wait")
	if err := runWithTimeout(context.Background(), 200*time.Millisecond, exe, args...); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("operation budget did not bound command", err)
	}
	exe, args = bootstrapCommand(t, "ok")
	if err := runWithTimeout(context.Background(), 15*time.Second, exe, args...); err != nil {
		t.Fatal("successful command failed", err)
	}
}
