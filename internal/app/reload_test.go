package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A collector and a worker reload their topology on SIGHUP. An edit that
// measures nothing new keeps every sample counting, even from a worker
// still on the old file; one that does is refused from that worker until
// it reloads too, and then counts under the new contract. A broken file
// or another project is refused, and the running topology kept.
func TestServeAndWorkerReloadOnHangup(t *testing.T) {
	dir, c, _, cr := initialized(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer target.Close()
	c.Endpoints[0].Address = target.URL
	c.Checks[0].IntervalSeconds = 1
	topology := filepath.Join(dir, "topology.json")
	saveConfig(t, topology, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveLog, serveHup := &lockedWriter{}, make(chan os.Signal, 1)
	collector := &App{In: strings.NewReader(""), Out: io.Discard, Err: serveLog, Reload: serveHup}
	served := make(chan error, 1)
	go func() {
		served <- collector.Run(ctx, []string{"serve", "--config", topology, "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "data"), "--listen", "127.0.0.1:0"})
	}()
	var addr string
	listening := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	testkit.Wait(t, 3*time.Second, func() bool {
		m := listening.FindStringSubmatch(serveLog.String())
		if len(m) > 1 {
			addr = m[1]
		}
		return addr != ""
	})
	workerCreds := filepath.Join(dir, "worker-credentials.json")
	wc, e := client.Load(workerCreds)
	if e != nil {
		t.Fatal(e)
	}
	wp := wc.Profiles["demo"]
	wp.Servers[0].URL = "http://" + addr
	wc.Profiles["demo"] = wp
	if e = client.Save(workerCreds, wc); e != nil {
		t.Fatal(e)
	}
	workerLog, workerHup := &lockedWriter{}, make(chan os.Signal, 1)
	worker := &App{In: strings.NewReader(""), Out: io.Discard, Err: workerLog, Reload: workerHup}
	worked := make(chan error, 1)
	go func() {
		worked <- worker.Run(ctx, []string{"worker", "--config", topology, "--observer", "demo", "--credentials", workerCreds, "--data", filepath.Join(dir, "worker-data")})
	}()
	vp := cr.Profiles["demo"]
	vp.Servers[0].URL = "http://" + addr
	viewer, e := client.New(vp)
	if e != nil {
		t.Fatal(e)
	}
	defer viewer.Close()
	// counted waits until the check is up with a sample taken after since,
	// under the contract the collector's topology gives it.
	counted := func(c model.Config, since time.Time) {
		t.Helper()
		testkit.Wait(t, 30*time.Second, func() bool {
			s, _, e := viewer.Snapshot(context.Background(), "")
			if e != nil || len(s.Checks) == 0 {
				return false
			}
			cs := s.Checks[0]
			return cs.Health == "up" && cs.Last != nil && cs.Last.SampledAt.After(since) && cs.Contract == config.PublicContracts(c)[cs.ID]
		})
	}
	// logged waits for a line in a log; a worker reports a refusal only
	// once it has retried the refused items one by one, a tick or two on.
	logged := func(w *lockedWriter, text string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for !strings.Contains(w.String(), text) {
			if time.Now().After(deadline) {
				t.Fatalf("no %q in the log:\n%s", text, w.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	counted(c, time.Time{})

	// A rename and a note: the collector reloads; the worker, still on the
	// old file, keeps delivering samples that count.
	renamed := c
	renamed.Nodes = append([]model.Node(nil), c.Nodes...)
	renamed.Nodes[0].Name, renamed.Nodes[0].Notes = "Renamed collector", "edited"
	saveConfig(t, topology, renamed)
	serveHup <- syscall.SIGHUP
	logged(serveLog, "every check measures what it did")
	counted(renamed, time.Now())
	if s, _, e := viewer.Snapshot(context.Background(), ""); e != nil || s.Topology.Nodes[0].Name != "Renamed collector" {
		t.Fatal("the reloaded topology is not served", e)
	}

	// A new assertion: the collector refuses the worker's samples of the
	// old contract until the worker reloads too.
	asserted := renamed
	asserted.Checks = append([]model.Check(nil), renamed.Checks...)
	asserted.Checks[0].HTTP.Contains = `"status"`
	saveConfig(t, topology, asserted)
	serveHup <- syscall.SIGHUP
	logged(serveLog, "1 check measures something new and start afresh: api-health")
	logged(workerLog, "contract_mismatch")
	workerHup <- syscall.SIGHUP
	logged(workerLog, "1 check measures something new and start afresh: api-health")
	counted(asserted, time.Now())

	// A broken file and another project are refused; both keep running.
	if e = os.WriteFile(topology, []byte("{broken"), 0600); e != nil {
		t.Fatal(e)
	}
	serveHup <- syscall.SIGHUP
	workerHup <- syscall.SIGHUP
	logged(serveLog, "reload refused; still serving revision "+config.Revision(asserted)[:12])
	logged(workerLog, "reload refused; still running revision "+config.Revision(asserted)[:12])
	other := asserted
	other.Project = "other-project"
	saveConfig(t, topology, other)
	serveHup <- syscall.SIGHUP
	logged(serveLog, `project "topophany-demo" cannot become "other-project"`)
	counted(asserted, time.Now())

	cancel()
	for _, done := range []chan error{served, worked} {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a process did not stop")
		}
	}
}
