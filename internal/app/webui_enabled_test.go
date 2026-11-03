//go:build webui

package app

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestCollectorWebLifecycle(t *testing.T) {
	dir, topology, _, credentials := initialized(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &lockedWriter{}
	done := make(chan error, 1)
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log}
	go func() {
		done <- a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "web-data"), "--listen", "127.0.0.1:0", "--web", "--web-listen", "127.0.0.1:0"})
	}()
	re := regexp.MustCompile(`Native Web UI listening on http://(127\.0\.0\.1:\d+)`)
	var addr string
	testkit.Wait(t, 5*time.Second, func() bool {
		m := re.FindStringSubmatch(log.String())
		if len(m) > 1 {
			addr = m[1]
		}
		return addr != ""
	})
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}

	post := func(edit api.ServiceEdit, want int) {
		t.Helper()
		body, _ := json.Marshal(edit)
		req, _ := http.NewRequest("POST", "http://"+addr+"/v1/web/service", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+credentials.Profiles["demo"].Servers[0].Token)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			raw, _ := io.ReadAll(response.Body)
			t.Fatalf("status %d: %s", response.StatusCode, raw)
		}
	}
	edit := api.ServiceEdit{ID: topology.Services[0].ID, Revision: config.Revision(config.Public(topology)), Name: "Renamed online", Environment: "production"}
	post(edit, 200)
	saved, err := config.Load(filepath.Join(dir, "topology.json"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Services[0].Name != edit.Name || saved.Services[0].Environment != edit.Environment {
		t.Fatal("edit not persisted")
	}
	post(edit, 409)
	edit.Revision = config.Revision(config.Public(saved))
	edit.Delete = true
	post(edit, 200)
	saved, err = config.Load(filepath.Join(dir, "topology.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range saved.Services {
		if service.ID == edit.ID {
			t.Fatal("service remains")
		}
		for _, ref := range service.DependsOn {
			if ref == edit.ID {
				t.Fatal("dependency remains")
			}
		}
	}
	for _, check := range saved.Checks {
		if check.Service == edit.ID {
			t.Fatal("check remains")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("collector/Web UI shutdown stalled")
	}
	if resp, err = client.Get("http://" + addr + "/"); err == nil {
		resp.Body.Close()
		t.Fatal("Web listener survived collector exit")
	}
}
func TestWebBuildDisabledDoesNotBind(t *testing.T) {
	enabled := false
	bad := "not-an-address"
	stop, err := New().startWeb(webFlags{enabled: &enabled, listen: &bad}, http.NotFoundHandler(), nil, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

func TestWebProxyFlags(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		wantError bool
	}{
		{nil, false},
		{[]string{"--web", "--web-proxy-origin", "https://pi.example.ts.net"}, false},
		{[]string{"--web-proxy-origin", "https://pi.example.ts.net"}, true},
		{[]string{"--web", "--web-proxy-origin", "http://pi.example.ts.net"}, true},
	} {
		f := flag.NewFlagSet("test", flag.ContinueOnError)
		web := addWebFlags(f)
		if err := f.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		if err := web.validate(); (err != nil) != tc.wantError {
			t.Fatalf("%v: unexpected validation result %v", tc.args, err)
		}
	}
}
