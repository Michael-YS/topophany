package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

const previewStreamRequest = `{"schema":1,"request_id":"one","source":"preview","interval":"1s","template":{"schema":1,"title":"Topophany","bar":{"row":"status"},"rows":[{"id":"status","metric":"summary.verdict","visual":"status"},{"id":"observers","metric":"observers.verdicts","visual":"list"},{"id":"attention","metric":"attention.list","visual":"list"},{"id":"applications","metric":"applications.list","visual":"list"},{"id":"server","metric":"server.card","visual":"card","select":{"node":"app"}}]}}`

func TestMonitorStreamClosedPipeErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"POSIX broken pipe", syscall.EPIPE, true},
		{"Windows broken pipe", syscall.Errno(109), runtime.GOOS == "windows"},
		{"Windows closing pipe", syscall.Errno(232), runtime.GOOS == "windows"},
		{"permission denied", os.ErrPermission, false},
		{"short write", io.ErrShortWrite, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStreamClosedPipe(tc.err); got != tc.want {
				t.Fatalf("closed pipe = %v, want %v", got, tc.want)
			}
			if tc.err != nil {
				wrapped := &os.PathError{Op: "write", Path: "stdout", Err: tc.err}
				if got := isStreamClosedPipe(wrapped); got != tc.want {
					t.Fatalf("wrapped closed pipe = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestMonitorStreamProtocol(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "topo")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../cmd/topo")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	type process struct {
		cmd    *exec.Cmd
		in     io.WriteCloser
		out    io.ReadCloser
		frames <-chan monitor.Panel
		stderr *bytes.Buffer
	}
	start := func(t *testing.T) *process {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		t.Cleanup(cancel)
		cmd := exec.CommandContext(ctx, binary, "monitor", "stream")
		cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		frames := make(chan monitor.Panel, 64)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { in.Close(); out.Close(); cmd.Process.Kill() })
		go func() {
			defer close(frames)
			scanner := bufio.NewScanner(out)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			for scanner.Scan() {
				var p monitor.Panel
				if err := json.Unmarshal(scanner.Bytes(), &p); err != nil {
					return
				}
				frames <- p
			}
		}()
		return &process{cmd, in, out, frames, stderr}
	}
	next := func(t *testing.T, p *process) monitor.Panel {
		t.Helper()
		select {
		case frame, ok := <-p.frames:
			if !ok {
				t.Fatal("stdout closed before expected frame")
			}
			return frame
		case <-time.After(6 * time.Second):
			t.Fatal("timed out waiting for frame")
			return monitor.Panel{}
		}
	}
	send := func(t *testing.T, p *process, s string) {
		t.Helper()
		if _, err := fmt.Fprintln(p.in, s); err != nil {
			t.Fatal(err)
		}
	}
	finish := func(t *testing.T, p *process) {
		t.Helper()
		if err := p.cmd.Wait(); err != nil {
			t.Fatal(err, p.stderr.String())
		}
		if p.stderr.Len() != 0 {
			t.Fatal(p.stderr.String())
		}
	}
	t.Run("server choices from a real preview stream", func(t *testing.T) {
		p := start(t)
		request := strings.Replace(previewStreamRequest, `"rows":[`, `"rows":[{"id":"choices","metric":"servers.list","visual":"list"},`, 1)
		send(t, p, request)
		frame := next(t, p)
		if frame.Lifecycle != "running" || len(frame.Rows) != 6 || frame.Rows[0].Metric != "servers.list" {
			t.Fatal(frame)
		}
		raw, err := json.Marshal(frame.Rows[0].Value)
		var servers []monitor.ServerListEntry
		if err != nil || json.Unmarshal(raw, &servers) != nil || len(servers) == 0 || len(servers) > 256 {
			t.Fatal(string(raw), err)
		}
		found := false
		for _, server := range servers {
			found = found || server.ID == "app"
		}
		if !found {
			t.Fatal("existing card missing from choices", servers)
		}
		p.in.Close()
		for frame := next(t, p); frame.Lifecycle != "stopped"; frame = next(t, p) {
		}
		finish(t, p)
	})
	t.Run("first replacement rejected request and EOF", func(t *testing.T) {
		p := start(t)
		select {
		case frame := <-p.frames:
			t.Fatal("frame before first request", frame)
		case <-time.After(100 * time.Millisecond):
		}
		send(t, p, previewStreamRequest)
		first := next(t, p)
		if first.Lifecycle != "running" || first.RequestID != "one" || len(first.Rows) != 5 || first.Rows[0].Value == nil {
			t.Fatal(first)
		}
		bad := strings.Replace(previewStreamRequest, "\"summary.verdict\"", "\"unknown.metric\"", 1)
		send(t, p, bad)
		invalid := next(t, p)
		if invalid.Lifecycle != "error" || invalid.Error == nil || invalid.Error.Code != "invalid_request" || len(invalid.Rows) != 0 {
			t.Fatal(invalid)
		}
		continued := next(t, p)
		if continued.Lifecycle != "running" || continued.RequestID != "one" || continued.ConfigRevision != first.ConfigRevision {
			t.Fatal(continued)
		}
		replacement := strings.Replace(previewStreamRequest, "\"one\"", "\"two\"", 1)
		replacement = strings.Replace(replacement, "\"source\":\"preview\"", "\"source\":\"preview\",\"scenario\":2", 1)
		send(t, p, replacement)
		second := next(t, p)
		for second.RequestID != "two" {
			second = next(t, p)
		}
		if second.Lifecycle != "running" || second.ConfigRevision == first.ConfigRevision || second.Instance != first.Instance || second.Sequence <= continued.Sequence {
			t.Fatal(second)
		}
		send(t, p, strings.Repeat("x", (256<<10)+1))
		if rejected := next(t, p); rejected.Error == nil || rejected.Error.Code != "invalid_request" {
			t.Fatal(rejected)
		}
		p.in.Close()
		stopped := next(t, p)
		for stopped.Lifecycle != "stopped" {
			stopped = next(t, p)
		}
		if stopped.RequestID != "two" || len(stopped.Rows) != 0 {
			t.Fatal(stopped)
		}
		finish(t, p)
	})
	t.Run("escaped invalid request cannot end stream", func(t *testing.T) {
		p := start(t)
		send(t, p, previewStreamRequest)
		next(t, p)
		send(t, p, `{"schema":1,"request_id":"`+strings.Repeat("<", 200000)+`"}`)
		rejected := next(t, p)
		if rejected.Lifecycle != "error" || rejected.Error == nil || rejected.Error.Code != "invalid_request" || len(rejected.RequestID) > 64 {
			t.Fatal(rejected)
		}
		send(t, p, strings.Replace(previewStreamRequest, `"one"`, `"two"`, 1))
		for frame := next(t, p); frame.RequestID != "two"; frame = next(t, p) {
			if frame.Lifecycle != "running" {
				t.Fatal(frame)
			}
		}
		p.in.Close()
		for frame := next(t, p); frame.Lifecycle != "stopped"; frame = next(t, p) {
		}
		finish(t, p)
	})
	t.Run("SIGTERM", func(t *testing.T) {
		if os.PathSeparator == '\\' {
			t.Skip("signal delivery is not available")
		}
		p := start(t)
		send(t, p, previewStreamRequest)
		next(t, p)
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		stopped := next(t, p)
		for stopped.Lifecycle != "stopped" {
			stopped = next(t, p)
		}
		if len(stopped.Rows) != 0 {
			t.Fatal(stopped)
		}
		finish(t, p)
	})
	t.Run("EOF without a request", func(t *testing.T) {
		p := start(t)
		p.in.Close()
		if stopped := next(t, p); stopped.Name != "stream" || stopped.Lifecycle != "stopped" || len(stopped.Rows) != 0 {
			t.Fatal(stopped)
		}
		finish(t, p)
	})
	t.Run("EPIPE", func(t *testing.T) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		reader.Close()
		defer writer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "monitor", "stream")
		cmd.Stdin = strings.NewReader(previewStreamRequest + "\n")
		cmd.Stdout = writer
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatal(err, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatal(stderr.String())
		}
	})
	t.Run("flags capabilities and runtime exit codes", func(t *testing.T) {
		for _, tc := range []struct {
			args  []string
			input string
			exit  int
		}{
			{[]string{"--invalid"}, "", 2},
			{[]string{"unexpected"}, "", 2},
			{nil, "not JSON\n", 0},
			{[]string{"--capabilities"}, "", 0},
		} {
			cmd := exec.Command(binary, append([]string{"monitor", "stream"}, tc.args...)...)
			cmd.Stdin = strings.NewReader(tc.input)
			output, err := cmd.Output()
			code := 0
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.exit {
				t.Fatal(tc.args, err)
			}
			if len(tc.args) > 0 && tc.args[0] == "--capabilities" {
				var caps struct {
					Protocol         int
					Sources, Visuals []string
					Metrics          []monitor.Metric
				}
				if bytes.Count(output, []byte{'\n'}) != 1 || json.Unmarshal(output, &caps) != nil || caps.Protocol != 1 || len(caps.Metrics) != len(monitor.Catalog()) || len(caps.Visuals) != 5 {
					t.Fatal(string(output))
				}
			}
		}
	})
}
