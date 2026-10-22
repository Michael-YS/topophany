package app

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/WhitenWhiten/topophany/internal/alert"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// A collector and a worker reload their topology on SIGHUP. An edit that
// measures nothing new keeps every sample counting, even from a worker
// still on the old file; one that does is refused from that worker until
// it reloads too, and then counts under the new contract. A broken file,
// another project or an invalid alerts file is refused, and the running
// topology kept.
func TestServeAndWorkerReloadOnHangup(t *testing.T) {
	dir, c, _, cr := initialized(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	t.Cleanup(target.Close)
	c.Endpoints[0].Address = target.URL
	c.Checks[0].IntervalSeconds = 1
	topology := filepath.Join(dir, "topology.json")
	saveConfig(t, topology, c)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hook := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(hook.Close)
	alerts := filepath.Join(dir, "alerts.json")
	writeAlerts := func(a alert.Config) {
		t.Helper()
		b, e := json.Marshal(a)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(alerts, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	writeAlerts(alert.Config{Schema: 1, Webhooks: []alert.Webhook{{ID: "hook", Format: "json", URL: hook.URL}}})

	type process struct {
		name string
		log  *lockedWriter
		done chan struct{}
		err  error // Read only after done is closed.
	}
	var processes []*process
	start := func(name string, app *App, log *lockedWriter, args ...string) {
		t.Helper()
		p := &process{name: name, log: log, done: make(chan struct{})}
		processes = append(processes, p)
		go func() {
			p.err = app.Run(ctx, args)
			close(p.done)
		}()
		// Registered after the dependencies and TempDir cleanups, so every
		// fatal path cancels and joins the processes before those are removed.
		t.Cleanup(func() {
			cancel()
			timer := time.NewTimer(15 * time.Second)
			defer timer.Stop()
			select {
			case <-p.done:
				if p.err != nil {
					t.Errorf("%s exited with an error: %v\n%s", p.name, p.err, p.log.String())
				}
			case <-timer.C:
				t.Errorf("%s did not stop within 15s after cancellation\n%s", p.name, p.log.String())
			}
		})
	}
	logs := func() string {
		var out strings.Builder
		for _, p := range processes {
			fmt.Fprintf(&out, "%s log:\n%s\n", p.name, p.log.String())
		}
		return out.String()
	}
	waitFor := func(description string, ready func(context.Context) bool) {
		t.Helper()
		// Startup includes durable store initialization. Give it the same
		// bounded allowance as reloads, while failing immediately on exit.
		waitCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			for _, p := range processes {
				select {
				case <-p.done:
					t.Fatalf("%s exited while waiting for %s: %v\n%s", p.name, description, p.err, logs())
				default:
				}
			}
			if ready(waitCtx) {
				return
			}
			select {
			case <-waitCtx.Done():
				t.Fatalf("timed out waiting for %s: %v\n%s", description, waitCtx.Err(), logs())
			case <-tick.C:
			}
		}
	}

	serveLog, serveHup := &lockedWriter{}, make(chan os.Signal, 1)
	collector := &App{In: strings.NewReader(""), Out: io.Discard, Err: serveLog, Reload: serveHup}
	start("collector", collector, serveLog, "serve", "--config", topology, "--auth", filepath.Join(dir, "auth.json"), "--alerts", alerts, "--data", filepath.Join(dir, "data"), "--listen", "127.0.0.1:0")
	var addr string
	listening := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	waitFor("collector listening", func(context.Context) bool {
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
	vp := cr.Profiles["demo"]
	vp.Servers[0].URL = "http://" + addr
	viewer, e := client.New(vp)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(viewer.Close)
	start("worker", worker, workerLog, "worker", "--config", topology, "--observer", "demo", "--credentials", workerCreds, "--data", filepath.Join(dir, "worker-data"))
	// counted waits until the check is up with a sample taken after since,
	// under the contract the collector's topology gives it.
	counted := func(c model.Config, since time.Time) {
		t.Helper()
		waitFor("a current sample under the expected contract", func(waitCtx context.Context) bool {
			s, _, e := viewer.Snapshot(waitCtx, "")
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
		waitFor(fmt.Sprintf("log entry %q", text), func(context.Context) bool {
			return strings.Contains(w.String(), text)
		})
	}
	// reports waits until the worker's observer says whether it measures
	// what the collector counts.
	reports := func(want string) {
		t.Helper()
		waitFor("observer contracts "+want, func(waitCtx context.Context) bool {
			s, _, e := viewer.Snapshot(waitCtx, "")
			return e == nil && len(s.Observers) == 1 && s.Observers[0].Contracts == want
		})
	}
	counted(c, time.Time{})
	reports("current")

	// A rename and a note: the collector reloads; the worker, still on the
	// old file, keeps delivering samples that count.
	renamed := c
	renamed.Nodes = append([]model.Node(nil), c.Nodes...)
	renamed.Nodes[0].Name, renamed.Nodes[0].Notes = "Renamed collector", "edited"
	saveConfig(t, topology, renamed)
	serveHup <- syscall.SIGHUP
	logged(serveLog, ", tokens and alerts; every check measures what it did")
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
	reports("outdated")
	workerHup <- syscall.SIGHUP
	logged(workerLog, "1 check measures something new and start afresh: api-health")
	counted(asserted, time.Now())
	reports("current")

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

	// So is an alerts file that does not validate, with a valid topology.
	saveConfig(t, topology, asserted)
	writeAlerts(alert.Config{Schema: 1})
	serveHup <- syscall.SIGHUP
	logged(serveLog, alerts+": alerts require schema 1")

	cancel()
	// Keep the normal shutdown assertion; cleanup also joins on any fatal
	// path, including a failure here, before removing files or dependencies.
	for _, p := range processes {
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatalf("%s exited with an error: %v\n%s", p.name, p.err, p.log.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not stop within 5s after cancellation\n%s", p.name, p.log.String())
		}
	}
}
