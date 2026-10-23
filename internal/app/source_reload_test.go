package app

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInvalidSourceReloadPreservesProcess(t *testing.T) {
	for _, role := range []string{"worker", "serve"} {
		t.Run(role, func(t *testing.T) {
			dir, c, _, _ := initialized(t)
			path := filepath.Join(dir, "topology.json")
			hup := make(chan os.Signal, 1)
			log := &lockedWriter{}
			a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log, Reload: hup}
			args := []string{role, "--config", path, "--observer", "demo", "--data", filepath.Join(dir, "runtime")}
			if role == "worker" {
				args = append(args, "--credentials", filepath.Join(dir, "worker-credentials.json"))
			} else {
				args = append(args, "--auth", filepath.Join(dir, "auth.json"), "--listen", "127.0.0.1:0")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() { result <- a.Run(ctx, args); close(done) }()
			defer func() { cancel(); <-done }()
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(log.String(), "Topophany") {
				select {
				case e := <-result:
					t.Fatalf("startup failed: %v", e)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("startup timeout")
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Generic config validation accepts this, but the installed Beszel adapter
			// requires a credential. Reload must reject it before retiring the old runtime.
			original := config.Clone(c)
			c.Sources = append(c.Sources, model.Source{ID: "bad-source", Adapter: "beszel", Observer: "demo", Target: "https://example.invalid", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180})
			c.Checks = append(c.Checks, model.Check{ID: "ext", Kind: "external", Observer: "demo", External: &model.ExternalBinding{Source: "bad-source", Object: "aaaaaaaaaaaaaaa", Report: model.ReportTypeResource, Node: "collector", MaxAgeSeconds: 180}})
			config.Defaults(&c)
			if e := config.Validate(c); e != nil {
				t.Fatal(e)
			}
			saveConfig(t, path, c)
			hup <- syscall.SIGHUP
			deadline = time.Now().Add(3 * time.Second)
			for !strings.Contains(log.String(), "reload refused") {
				select {
				case e := <-result:
					t.Fatalf("invalid reload terminated %s: %v\n%s", role, e, log.String())
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("reload refusal not reported")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if strings.Contains(log.String(), "reloaded topology") {
				t.Fatal("invalid configuration published")
			}
			original.Nodes[0].Name = "valid reload"
			saveConfig(t, path, original)
			hup <- syscall.SIGHUP
			for !strings.Contains(log.String(), "reloaded topology") {
				select {
				case e := <-result:
					t.Fatal(e)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("subsequent valid reload failed")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}
