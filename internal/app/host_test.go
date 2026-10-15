//go:build linux || darwin

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestHostWorkerIngestionAndCLIRead(t *testing.T) {
	dir, c, auth, viewer := initialized(t)
	c.Checks = []model.Check{{ID: "host-resources", Observer: "demo", Kind: "host", Host: &model.HostOptions{DiskPath: t.TempDir(), ProcessName: "topo-no-such", MinProcesses: 1}}}
	config.Defaults(&c)
	path := filepath.Join(dir, "topology.json")
	saveConfig(t, path, c)
	// Native one-shot execution uses the same probe as worker and serve.
	out, _, err := invoke("", "once", "--config", path, "--observer", "demo")
	var once model.Observation
	if err != nil || json.Unmarshal([]byte(out), &once) != nil || once.Outcome != "policy" || once.Facts["memory_total_bytes"] == "" {
		t.Fatal(err, out)
	}
	st, err := store.Open(filepath.Join(dir, "server-data"), store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(api.New(c, "remote", st, auth).Handler())
	defer srv.Close()
	credPath := filepath.Join(dir, "worker-credentials.json")
	cr, err := client.Load(credPath)
	if err != nil {
		t.Fatal(err)
	}
	p := cr.Profiles["demo"]
	p.Servers[0].URL = srv.URL
	cr.Profiles["demo"] = p
	if err := client.Save(credPath, cr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: &lockedWriter{}}
	go func() {
		done <- a.Run(ctx, []string{"worker", "--config", path, "--observer", "demo", "--credentials", credPath, "--data", filepath.Join(dir, "worker-data")})
	}()
	testkit.Wait(t, 10*time.Second, func() bool {
		rows, _ := st.View()
		return len(rows) > 0 && rows[0].Outcome == "policy" && rows[0].Evidence == "reported" && rows[0].Facts["cpu_used_percent"] != ""
	})
	vp := viewer.Profiles["demo"]
	vp.Servers[0].URL = srv.URL
	viewer.Profiles["demo"] = vp
	viewPath := filepath.Join(dir, "credentials.json")
	if err := client.Save(viewPath, viewer); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show", "checks", "host-resources"}, {"history", "host-resources", "--json"}} {
		out, _, err := invoke("", append(args, "--credentials", viewPath)...)
		if err != nil || !strings.Contains(out, "memory_total_bytes") || !strings.Contains(out, "process_matching_count") {
			t.Fatal(args, err, out)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("host worker did not stop")
	}
}
