package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestMonitorScopedClientPublishesObserverVerdicts(t *testing.T) {
	c := testkit.Config()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("observer") != "home" {
			t.Error("unexpected extra scope read", r.URL)
		}
		now := time.Now().UTC()
		var samples []model.Observation
		for i, x := range c.Checks {
			samples = append(samples, testkit.Observation(c, x.ID, uint64(i+1), now, "ok"))
		}
		json.NewEncoder(w).Encode(engine.Build(c, "fixture", "home", samples, nil, now))
	}))
	defer server.Close()
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials.json")
	if err := client.Save(credentials, client.Credentials{Schema: 1, Active: "default", Profiles: map[string]client.Profile{"default": {Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: strings.Repeat("a", 64)}}}}}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "metrics.json")
	root := filepath.Join(dir, "panels")
	template := `{"schema":1,"title":"Scoped","bar":{"row":"s"},"rows":[{"id":"s","metric":"observers.verdicts","visual":"list"}]}`
	if _, _, err := invoke(template, "monitor", "configure", "desktop", "--monitor-dir", root, "--template", "-", "--credentials", credentials, "--observer", "home", "--output", output, "--interval", "1s"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() {
		a := &App{Out: io.Discard, Err: io.Discard}
		done <- a.Run(ctx, []string{"monitor", "run", "desktop", "--monitor-dir", root})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	testkit.Wait(t, 3*time.Second, func() bool {
		raw, err := os.ReadFile(output)
		if err != nil {
			return false
		}
		var p monitor.Panel
		if json.Unmarshal(raw, &p) != nil || len(p.Rows) == 0 || p.Rows[0].Value == nil {
			return false
		}
		data, _ := json.Marshal(p.Rows[0].Value)
		var observers []monitor.ObserverVerdict
		if json.Unmarshal(data, &observers) != nil {
			return false
		}
		for _, o := range observers {
			if o.ID == "home" {
				if !o.Live || o.State != "clear" || o.Counts.Up != 5 {
					t.Errorf("scoped panel lost live evidence: %+v", o)
				}
				return true
			}
		}
		return false
	})
}
