package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/client"
)

func TestMonitorProfilesDiscovery(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("AppData", root)
	path, err := client.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	contents := `{"schema":1,"active":"home","profiles":{"work":{"project":"","servers":[{"url":"https://private.invalid/user","token":"private-token"}]},"home":{"project":"homelab"}}}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"monitor", "stream", "--profiles"},
		{"monitor", "stream", "--profiles", "--credentials", path},
	} {
		out, stderr, err := invoke("not a stream request", args...)
		var got map[string][]map[string]any
		if err != nil || stderr != "" || strings.Count(out, "\n") != 1 || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 || len(got["profiles"]) != 2 {
			t.Fatal(out, stderr, err)
		}
		profiles := got["profiles"]
		if profiles[0]["name"] != "home" || profiles[0]["project"] != "homelab" || profiles[0]["active"] != true || profiles[1]["name"] != "work" || profiles[1]["project"] != "" || profiles[1]["active"] != false {
			t.Fatal(out)
		}
		for _, p := range profiles {
			if len(p) != 3 {
				t.Fatal("unexpected metadata fields", out)
			}
		}
		if strings.Contains(out+stderr, "private") || strings.Contains(out+stderr, path) {
			t.Fatal("discovery disclosed private input", out, stderr)
		}
	}
	// An explicit path takes precedence over a populated default file.
	explicit := filepath.Join(root, "other.json")
	if err := os.WriteFile(explicit, []byte(`{"schema":1,"profiles":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := invoke("", "monitor", "stream", "--profiles", "--credentials", explicit)
	if err != nil || stderr != "" || out != "{\"profiles\":[]}\n" {
		t.Fatal(out, stderr, err)
	}
}

func TestMonitorProfilesUnavailableAndFlags(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		missing, bad  bool
	}{
		{name: "missing", missing: true},
		{name: "empty", content: `{"schema":1,"profiles":{}}`},
		{name: "malformed", content: `{"secret":"private-token"`, bad: true},
		{name: "bad schema", content: `{"schema":99}`, bad: true},
		{name: "bad identity", content: `{"schema":1,"active":"private-token","profiles":{"bad name":{}}}`, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-path.json")
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, stderr, err := invoke("", "monitor", "stream", "--profiles", "--credentials", path)
			if err != nil || out != "{\"profiles\":[]}\n" || tc.bad && stderr != "monitor stream profiles unavailable\n" || !tc.bad && stderr != "" {
				t.Fatal(out, stderr, err)
			}
		})
	}
	for _, args := range [][]string{
		{"--capabilities", "--profiles"},
		{"--credentials", "private-path"},
		{"--capabilities", "--credentials", "private-path"},
		{"--profiles", "unexpected"},
	} {
		out, _, err := invoke("", append([]string{"monitor", "stream"}, args...)...)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != 2 || out != "" {
			t.Fatal(args, out, err)
		}
	}
}

type discoverySink struct {
	bytes.Buffer
	writes, flushes int
	err             error
}

func (s *discoverySink) Write(b []byte) (int, error) {
	s.writes++
	if s.err != nil {
		return 0, s.err
	}
	return s.Buffer.Write(b)
}
func (s *discoverySink) Flush() error { s.flushes++; return nil }

func TestMonitorDiscoveryWriteFlushAndClosedPipe(t *testing.T) {
	for _, args := range [][]string{{"--capabilities"}, {"--profiles", "--credentials", filepath.Join(t.TempDir(), "missing")}} {
		for _, failure := range []error{nil, syscall.EPIPE, io.ErrShortWrite} {
			out := &discoverySink{err: failure}
			var stderr bytes.Buffer
			a := &App{In: strings.NewReader(""), Out: out, Err: &stderr}
			err := a.Run(context.Background(), append([]string{"monitor", "stream"}, args...))
			if err != nil || out.writes != 1 || failure == nil && (out.flushes != 1 || bytes.Count(out.Bytes(), []byte{'\n'}) != 1) || failure != nil && out.flushes != 0 {
				t.Fatal(args, failure, out, err)
			}
			if failure == io.ErrShortWrite && stderr.String() != "monitor stream output unavailable\n" || failure != io.ErrShortWrite && stderr.Len() != 0 {
				t.Fatal(stderr.String())
			}
		}
	}
}
