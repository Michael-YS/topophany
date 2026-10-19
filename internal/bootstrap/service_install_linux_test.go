//go:build linux

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceStartAndRestoreCommands(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TOPO_TEST_SERVICE_LOG\"\n[ \"$1\" != \"$TOPO_TEST_SERVICE_FAIL\" ]\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TOPO_TEST_SERVICE_LOG", log)
	t.Setenv("TOPO_TEST_SERVICE_FAIL", "")
	m := Manifest{Role: "worker", Service: "topophany-worker.service"}
	ctx := context.Background()
	if err := startService(ctx, m, nil); err != nil {
		t.Fatal(err)
	}
	if err := restoreService(ctx, installation{Manifest: m, WasEnabled: true, WasActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := restoreService(ctx, installation{Manifest: m}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	want := "daemon-reload\nenable --now topophany-worker.service\ndaemon-reload\nenable topophany-worker.service\nstart topophany-worker.service\ndaemon-reload\ndisable topophany-worker.service\n"
	if err != nil || string(raw) != want {
		t.Fatal("unexpected service lifecycle", err, string(raw))
	}
	t.Setenv("TOPO_TEST_SERVICE_FAIL", "daemon-reload")
	if err := startService(ctx, m, nil); err == nil {
		t.Fatal("failed reload accepted")
	}
	if err := restoreService(ctx, installation{Manifest: m}); err == nil {
		t.Fatal("failed restore reload accepted")
	}
	after, err := os.ReadFile(log)
	if err != nil || !strings.HasSuffix(string(after), "daemon-reload\ndaemon-reload\n") {
		t.Fatal("failure continued service mutation", err, string(after))
	}
}
