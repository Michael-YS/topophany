package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func invoke(input string, args ...string) (string, string, error) {
	var out, stderr bytes.Buffer
	a := &App{In: strings.NewReader(input), Out: &out, Err: &stderr}
	e := a.Run(context.Background(), args)
	return out.String(), stderr.String(), e
}
func initialized(t *testing.T) (string, model.Config, api.Auth, client.Credentials) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	if _, _, e := invoke("", "init", "--dir", dir); e != nil {
		t.Fatal(e)
	}
	c, e := config.Load(filepath.Join(dir, "topology.json"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := api.LoadAuth(filepath.Join(dir, "auth.json"), c)
	if e != nil {
		t.Fatal(e)
	}
	cr, e := client.Load(filepath.Join(dir, "credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	return dir, c, a, cr
}
func saveConfig(t *testing.T, path string, c model.Config) {
	t.Helper()
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = secure.AtomicWrite(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestCLIDispatchAndHelp(t *testing.T) {
	out, _, e := invoke("", "--help")
	if e != nil || !strings.Contains(out, "topo --credential TOKEN") {
		t.Fatal(e, out)
	}
	out, _, e = invoke("", "version")
	if e != nil || !strings.Contains(out, "Topophany "+model.Version) {
		t.Fatal(e, out)
	}
	for _, cmd := range []string{"serve", "worker", "login", "init", "validate", "export", "once", "token", "edit", "preview", "status", "show", "history"} {
		_, stderr, e := invoke("", cmd, "--help")
		if !errors.Is(e, flag.ErrHelp) || !strings.Contains(stderr, "Usage of") {
			t.Fatal(cmd, e, stderr)
		}
	}
	for _, args := range [][]string{{"missing"}, {"--unknown"}, {"token", "extra"}, {"validate", "--config", "missing"}} {
		if _, _, e = invoke("", args...); e == nil {
			t.Fatal("bad command accepted", args)
		}
	}
	out, _, e = invoke("", "token")
	var token map[string]string
	if e != nil || json.Unmarshal([]byte(out), &token) != nil || secure.Hash(token["token"]) != token["token_sha256"] {
		t.Fatal(e, out)
	}
}
func TestInitFreshCredentialsNoOverwriteAndSanitizedExport(t *testing.T) {
	dir, c, a, cr := initialized(t)
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	v := cr.Profiles["demo"].Servers[0].Token
	if a.Tokens[0].TokenSHA256 != secure.Hash(v) {
		t.Fatal("auth/credential mismatch")
	}
	worker, e := client.Load(filepath.Join(dir, "worker-credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	if worker.Profiles["demo"].Servers[0].Token == v {
		t.Fatal("roles share token")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if strings.Contains(string(raw), v) {
		t.Fatal("plaintext token in auth")
	}
	_, _, _, cr2 := initialized(t)
	if v == cr2.Profiles["demo"].Servers[0].Token {
		t.Fatal("hardcoded credentials")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if _, _, e = invoke("", "init", "--dir", dir); e == nil {
		t.Fatal("init overwrote credentials")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("init modified existing")
	}
	out, _, e := invoke("", "validate", "--config", filepath.Join(dir, "topology.json"))
	if e != nil || !strings.Contains(out, config.Revision(c)) {
		t.Fatal(e, out)
	}
	out, _, e = invoke("", "export", "--config", filepath.Join(dir, "topology.json"))
	if e != nil || strings.Contains(out, v) {
		t.Fatal(e)
	}
	for _, x := range c.Checks {
		if !strings.Contains(config.Target(c, x), "127.0.0.1") {
			t.Fatal("demo performs external probes")
		}
	}
}
func TestLoginRemoteJSONAndSavedDefault(t *testing.T) {
	dir, c, a, cr := initialized(t)
	st, e := store.Open(filepath.Join(dir, "collector-test"), store.Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	srv := httptest.NewServer(api.New(c, "remote", st, a).Handler())
	defer srv.Close()
	token := cr.Profiles["demo"].Servers[0].Token
	credpath := filepath.Join(dir, "new-client.json")
	out, _, e := invoke(token+"\n", "login", "--server", srv.URL, "--project", c.Project, "--credentials", credpath, "--token-stdin")
	if e != nil || !strings.Contains(out, "Saved verified project") {
		t.Fatal(e, out)
	}
	out, _, e = invoke("", "--credentials", credpath, "--json")
	if e != nil {
		t.Fatal(e)
	}
	var snap model.Snapshot
	if json.Unmarshal([]byte(out), &snap) != nil || snap.Instance != "remote" {
		t.Fatal(out)
	}
	if _, _, e = invoke("", "--credentials", credpath, "--json", "--credential", strings.Repeat("e", 64)); e == nil {
		t.Fatal("bad override not checked")
	}
	if _, _, e = invoke("", "--credentials", credpath, "--profile", "missing", "--json"); e == nil {
		t.Fatal("unknown profile")
	}
	out, _, e = invoke("", "--credentials", filepath.Join(dir, "missing"), "--server", srv.URL, "--project", c.Project, "--credential", token, "--json")
	if e != nil || !strings.Contains(out, "remote") {
		t.Fatal(e)
	}
	bad := filepath.Join(dir, "bad.json")
	secure.AtomicWrite(bad, []byte("corrupt"), 0600)
	if _, _, e = invoke(token, "login", "--server", srv.URL, "--project", c.Project, "--credentials", bad, "--token-stdin"); e == nil {
		t.Fatal("login replaced corrupt credentials")
	}
	raw, _ := os.ReadFile(bad)
	if string(raw) != "corrupt" {
		t.Fatal("modified corrupt file")
	}
	for _, tc := range []struct{ project, token string }{{"other", token}, {c.Project, "bad"}, {c.Project, strings.Repeat("z", 1025)}} {
		if _, _, e = invoke(tc.token, "login", "--server", srv.URL, "--project", tc.project, "--credentials", filepath.Join(dir, "must-not-exist"), "--token-stdin"); e == nil {
			t.Fatal("invalid login")
		}
	}
	if _, e = os.Stat(filepath.Join(dir, "must-not-exist")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("failed login wrote credentials")
	}
	if _, _, e = invoke("", "--credentials", credpath); e == nil {
		t.Fatal("nonterminal UI accepted")
	}
}
func TestOnceRealLocalHTTP(t *testing.T) {
	dir, c, _, _ := initialized(t)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer httpServer.Close()
	c.Endpoints[0].Address = httpServer.URL
	saveConfig(t, filepath.Join(dir, "once.json"), c)
	out, _, e := invoke("", "once", "--config", filepath.Join(dir, "once.json"), "--observer", "demo")
	if e != nil {
		t.Fatal(e)
	}
	var v model.Observation
	if json.Unmarshal([]byte(out), &v) != nil || v.Outcome != "ok" || v.Observer != "demo" || v.Sequence != 1 || v.ID == "" {
		t.Fatal(out)
	}
	if _, _, e = invoke("", "once", "--config", filepath.Join(dir, "once.json")); e == nil {
		t.Fatal("once without observer")
	}
}

type lockedWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(b)
}
func (w *lockedWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }
func TestServeActualHTTPAndGracefulShutdown(t *testing.T) {
	dir, c, _, cr := initialized(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer target.Close()
	c.Endpoints[0].Address = target.URL
	c.Checks[0].IntervalSeconds = 1
	saveConfig(t, filepath.Join(dir, "topology.json"), c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &lockedWriter{}
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log}
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "data"), "--listen", "127.0.0.1:0", "--observer", "demo"})
	}()
	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	var addr string
	testkit.Wait(t, 3*time.Second, func() bool {
		m := re.FindStringSubmatch(log.String())
		if len(m) > 1 {
			addr = m[1]
			return true
		}
		return false
	})
	p := cr.Profiles["demo"]
	p.Servers[0].URL = "http://" + addr
	cl, e := client.New(p)
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	testkit.Wait(t, 3*time.Second, func() bool {
		s, _, e := cl.Snapshot(context.Background(), "demo")
		return e == nil && len(s.Checks) > 0 && s.Checks[0].Health == "up"
	})
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve shutdown leaked goroutines")
	}
	st, e := store.Open(filepath.Join(dir, "data"), store.Options{Revision: config.Revision(c)})
	if e != nil {
		t.Fatal("server did not release journal", e)
	}
	st.Close()
}
func TestWorkerActualRemoteIngestion(t *testing.T) {
	dir, c, auth, _ := initialized(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer target.Close()
	c.Endpoints[0].Address = target.URL
	c.Checks[0].IntervalSeconds = 1
	saveConfig(t, filepath.Join(dir, "topology.json"), c)
	st, e := store.Open(filepath.Join(dir, "server-data"), store.Options{Revision: config.Revision(c)})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	srv := httptest.NewServer(api.New(c, "remote", st, auth).Handler())
	defer srv.Close()
	credPath := filepath.Join(dir, "worker-credentials.json")
	cr, e := client.Load(credPath)
	if e != nil {
		t.Fatal(e)
	}
	p := cr.Profiles["demo"]
	p.Servers[0].URL = srv.URL
	cr.Profiles["demo"] = p
	client.Save(credPath, cr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	app := &App{In: strings.NewReader(""), Out: io.Discard, Err: &lockedWriter{}}
	go func() {
		done <- app.Run(ctx, []string{"worker", "--config", filepath.Join(dir, "topology.json"), "--observer", "demo", "--credentials", credPath, "--data", filepath.Join(dir, "worker-data")})
	}()
	testkit.Wait(t, 5*time.Second, func() bool {
		rows, beats := st.View()
		return len(rows) > 0 && rows[0].Outcome == "ok" && beats["demo"].Observer == "demo"
	})
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}
func TestServeTransportAndArgumentGuards(t *testing.T) {
	for _, args := range [][]string{{"--listen", "0.0.0.0:7447"}, {"--listen", "broken"}, {"--tls-cert", "one.pem"}, {"--instance", "../bad"}, {"--retention", "1s"}, {"--retention", "9999h"}} {
		_, _, e := invoke("", append([]string{"serve"}, args...)...)
		if e == nil {
			t.Fatal(args)
		}
	}
	_, _, e := invoke("", "login", "--project", "wrong id")
	if e == nil {
		t.Fatal("invalid project")
	}
	a := New()
	if a.In == nil || a.Out == nil || a.TerminalIn == nil {
		t.Fatal("default streams")
	}
	_ = fmt.Sprint(a)
}

func TestShippedTopologyExamplesValidateAndHomelabIsPaused(t *testing.T) {
	for _, name := range []string{"minimal.json", "homelab.json"} {
		t.Run(name, func(t *testing.T) {
			c, err := config.Load(filepath.Join("..", "..", "examples", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := config.Validate(c); err != nil {
				t.Fatal(err)
			}
			if name == "homelab.json" {
				if len(c.Nodes) < 10 || len(c.Checks) < 20 {
					t.Fatal("homelab example lost scenarios")
				}
				for _, check := range c.Checks {
					if !check.Disabled {
						t.Fatalf("production template enabled %s", check.ID)
					}
				}
			}
		})
	}
}
