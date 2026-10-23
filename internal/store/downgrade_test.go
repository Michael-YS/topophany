package store

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// oldFormatCommit is the baseline reviewed before the external store format
// existed. The downgrade test extracts this exact commit, builds its real
// store reader, and asks it to open data written by this tree. It never uses a
// generated fixture from /tmp.
const oldFormatCommit = "74db358929247e2bf38b219a3682b34e9a3c4bf2"

func oldReaderUnavailable(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("TOPO_REQUIRE_OLD_READER") == "1" {
		t.Fatal(reason)
	}
	t.Skip(reason)
}

func buildOldFormatReader(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		oldReaderUnavailable(t, "git unavailable; cannot build the fixed old store reader")
	}
	if _, err := exec.LookPath("go"); err != nil {
		oldReaderUnavailable(t, "go unavailable; cannot build the fixed old store reader")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	check := exec.Command("git", "-C", root, "cat-file", "-e", oldFormatCommit+"^{commit}")
	if err := check.Run(); err != nil {
		oldReaderUnavailable(t, "commit "+oldFormatCommit+" is not present in this checkout")
	}
	dir := t.TempDir()
	archive := exec.Command("git", "-C", root, "archive", "--format=tar", oldFormatCommit)
	stream, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Start(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(stream)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, filepath.Clean(h.Name))
		if !strings.HasPrefix(target, dir+string(filepath.Separator)) && target != dir {
			t.Fatalf("archive path escapes destination: %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Wait after draining the pipe so git cannot block on a full stderr pipe.
	if err := archive.Wait(); err != nil {
		t.Fatalf("git archive %s: %v", oldFormatCommit, err)
	}
	main := `package main

import (
	"fmt"
	"os"

	"github.com/WhitenWhiten/topophany/internal/store"
)

func main() {
	s, err := store.Open(os.Args[1], store.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "refused:", err)
		os.Exit(1)
	}
	if err := s.Checkpoint(); err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint:", err)
		os.Exit(2)
	}
	if err := s.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close:", err)
		os.Exit(3)
	}
}
`
	cmdDir := filepath.Join(dir, "cmd", "oldformatcheck")
	if err := os.MkdirAll(cmdDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "oldformatcheck")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", out, "./cmd/oldformatcheck")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOCACHE="+filepath.Join(dir, "gocache"))
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixed old reader from %s: %v\n%s", oldFormatCommit, err, raw)
	}
	return out
}

func runOldReader(t *testing.T, binary, dir string) (int, string) {
	t.Helper()
	cmd := exec.Command(binary, dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("run old reader: %v", err)
	return 0, ""
}

func checkpointSchema(t *testing.T, dir string) (int, *model.ExternalState) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := unwrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Schema   int                  `json:"schema"`
		External *model.ExternalState `json:"external"`
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatal(err)
	}
	return state.Schema, state.External
}

func TestOldBinaryRefusesNewExternalData(t *testing.T) {
	old := buildOldFormatReader(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	t.Run("automatic checkpoint retains barrier", func(t *testing.T) {
		c := externalStoreConfig(t)
		opts := externalStoreOptions(c, &now)
		opts.CheckpointBytes, opts.MaxJournalBytes = 1024, 64<<10
		s := openTest(t, t.TempDir(), opts)
		v := externalStoreObservation(t, c, "sample-1", 25, 1, now, now)
		v.Detail = strings.Repeat("x", 800)
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if code, out := runOldReader(t, old, s.dir); code == 0 || !strings.Contains(out, "refused:") {
			t.Fatalf("old reader did not refuse automatic checkpoint: %d %s", code, out)
		}
	})

	t.Run("native only remains readable", func(t *testing.T) {
		c := testkit.Config()
		config.Defaults(&c)
		dir := t.TempDir()
		s := openTest(t, dir, Options{}.WithTopology(c))
		if err := s.Put(batch(c, testkit.Observation(c, "home-http", 1, now, "ok"))); err != nil {
			t.Fatal(err)
		}
		if err := s.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if schema, _ := checkpointSchema(t, dir); schema != 1 {
			t.Fatalf("native-only checkpoint schema = %d", schema)
		}
		if code, out := runOldReader(t, old, dir); code != 0 {
			t.Fatalf("old binary could not read native data: exit %d\n%s", code, out)
		}
	})

	t.Run("first external write and checkpoint", func(t *testing.T) {
		c := externalStoreConfig(t)
		dir := t.TempDir()
		s := openTest(t, dir, externalStoreOptions(c, &now))
		if err := s.Put(batch(c, externalStoreObservation(t, c, "sample-1", 25, 1, now, now))); err != nil {
			t.Fatal(err)
		}
		// The barrier checkpoint exists before this Put was acknowledged.
		if schema, _ := checkpointSchema(t, dir); schema != 2 {
			t.Fatalf("barrier checkpoint schema = %d", schema)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if code, out := runOldReader(t, old, dir); code == 0 {
			t.Fatalf("old binary accepted schema 2 external WAL\n%s", out)
		} else if !strings.Contains(out, "refused:") {
			t.Fatalf("old reader failed for the wrong reason: %s", out)
		}

		// An explicit checkpoint keeps the same barrier.
		reopened := openTest(t, dir, externalStoreOptions(c, &now))
		if err := reopened.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
		if schema, _ := checkpointSchema(t, dir); schema != 2 {
			t.Fatalf("post-checkpoint schema = %d", schema)
		}
		if code, out := runOldReader(t, old, dir); code == 0 {
			t.Fatalf("old binary accepted schema 2 checkpoint\n%s", out)
		}

		// Removing the external source and state cannot write the old schema
		// back: the format barrier is monotonic.
		without := config.Clone(c)
		without.Checks = without.Checks[:len(without.Checks)-1]
		config.Defaults(&without)
		if err := config.Validate(without); err != nil {
			t.Fatal(err)
		}
		reopened = openTest(t, dir, externalStoreOptions(without, &now))
		if err := reopened.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
		schema, state := checkpointSchema(t, dir)
		if schema != 2 || state != nil && len(state.Bindings) > 0 {
			t.Fatalf("monotonic format after source removal: schema=%d state=%+v", schema, state)
		}
		if code, out := runOldReader(t, old, dir); code == 0 {
			t.Fatalf("old binary accepted a downgraded directory\n%s", out)
		}
	})
}

func TestExternalFormatBarrierFailureLeavesNoAck(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openTest(t, dir, externalStoreOptions(c, &now))
	previous := installCheckpoint
	t.Cleanup(func() { installCheckpoint = previous })
	installCheckpoint = func(string, func(io.WriteSeeker) error) error { return os.ErrPermission }
	err := s.Put(batch(c, externalStoreObservation(t, c, "sample-1", 25, 1, now, now)))
	installCheckpoint = previous
	if err == nil || !strings.Contains(err.Error(), "format barrier") {
		t.Fatalf("failed barrier was acknowledged: %v", err)
	}
	if state := s.ExternalView(); state != nil && len(state.Bindings) > 0 {
		t.Fatalf("failed barrier installed external state: %+v", state)
	}
	if s.bytes != 0 {
		t.Fatalf("failed barrier wrote %d journal bytes", s.bytes)
	}
	// The store is poisoned by a failed durability operation; restart recovers
	// the old state and the retry can install the barrier.
	_ = s.Close()
	reopened := openTest(t, dir, externalStoreOptions(c, &now))
	if err := reopened.Put(batch(c, externalStoreObservation(t, c, "sample-1", 25, 1, now, now))); err != nil {
		t.Fatalf("retry after barrier failure: %v", err)
	}
	if state := reopened.ExternalView(); state == nil || state.Bindings["vm-resources"].ReportSamples != 1 {
		t.Fatalf("retry state = %+v", state)
	}
	if schema, _ := checkpointSchema(t, dir); schema != 2 {
		t.Fatalf("retry checkpoint schema = %d", schema)
	}
}

func TestLegacyExternalJournalUpgradesBeforeNextAck(t *testing.T) {
	c := externalStoreConfig(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	previous := installCheckpoint
	t.Cleanup(func() { installCheckpoint = previous })

	// Simulate data written before the format barrier existed: an external
	// journal with no schema 2 checkpoint.
	installCheckpoint = func(string, func(io.WriteSeeker) error) error { return nil }
	legacy := openTest(t, dir, externalStoreOptions(c, &now))
	if err := legacy.Put(batch(c, externalStoreObservation(t, c, "sample-1", 25, 1, now, now))); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	installCheckpoint = previous
	if _, err := os.Stat(filepath.Join(dir, "checkpoint.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy setup unexpectedly wrote a checkpoint: %v", err)
	}
	journal, err := os.Stat(filepath.Join(dir, "journal.jsonl"))
	if err != nil || journal.Size() == 0 {
		t.Fatalf("legacy journal missing: %v (%d)", err, journal.Size())
	}

	// An interrupted upgrade must not acknowledge or discard the legacy
	// record; reopening retries the checkpoint install.
	installCheckpoint = func(string, func(io.WriteSeeker) error) error { return os.ErrPermission }
	if _, err := Open(dir, externalStoreOptions(c, &now)); err == nil || !strings.Contains(err.Error(), "format upgrade") {
		t.Fatalf("interrupted upgrade outcome = %v", err)
	}
	installCheckpoint = previous
	upgraded, err := Open(dir, externalStoreOptions(c, &now))
	if err != nil {
		t.Fatalf("upgrade retry: %v", err)
	}
	defer upgraded.Close()
	state := upgraded.ExternalView()
	if state == nil || state.Bindings["vm-resources"].ReportSamples != 1 {
		t.Fatalf("upgrade lost external state: %+v", state)
	}
	if schema, _ := checkpointSchema(t, dir); schema != 2 {
		t.Fatalf("upgraded schema = %d", schema)
	}
}
