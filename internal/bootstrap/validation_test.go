package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/client"
)

func TestBundleReviewRejectsInvalidObserver(t *testing.T) {
	for _, role := range []string{"server", "worker"} {
		t.Run(role, func(t *testing.T) {
			o := options(t, role)
			o.Observer = "home"
			if role == "worker" {
				var err error
				o.CA, _, _, _, err = certificates([]string{"localhost"})
				if err != nil {
					t.Fatal(err)
				}
				o.Profile = client.Profile{Project: o.Config.Project, Servers: []client.Endpoint{{URL: o.Server, Token: strings.Repeat("a", 64)}}}
			}
			dir := filepath.Join(t.TempDir(), "bundle")
			m, err := Generate(dir, o)
			if err != nil {
				t.Fatal(err)
			}
			invalid := []string{"undeclared", "home\n[Service]", "home\r", "home\x00"}
			if role == "worker" {
				invalid = append(invalid, "")
			}
			for _, id := range invalid {
				m.Observer = id
				raw, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
				if _, _, err = Review(dir); err == nil || !strings.Contains(err.Error(), "observer") {
					t.Fatalf("observer %q: %v", id, err)
				}
			}
		})
	}
}

func TestServiceRejectsControlCharacters(t *testing.T) {
	for _, control := range []string{"\n", "\r", "\x00", "\t", "\x1b", "\u0085"} {
		if _, err := unitQuote("before" + control + "after"); err == nil {
			t.Fatalf("unit quote accepted %q", control)
		}
		for _, platform := range []string{"linux", "darwin", "windows"} {
			o := options(t, "worker")
			o.Platform, o.Observer = platform, "home"+control
			if _, _, _, err := service(o); err == nil {
				t.Fatalf("%s accepted control %q", platform, control)
			}
		}
	}
}

func TestPendingOutboxMustBeReadableAndEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending")
	if err := checkPendingOutbox(path); err != nil {
		t.Fatal("missing outbox", err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkPendingOutbox(path); err != nil {
		t.Fatal("empty outbox", err)
	}
	if err := os.WriteFile(filepath.Join(path, "sample.json"), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPendingOutbox(path); err == nil {
		t.Fatal("nonempty outbox accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPendingOutbox(file); err == nil {
		t.Fatal("unreadable outbox treated as empty")
	}
}

func TestPrepareDataDirectoryCreatesPrivateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "data")
	if err := prepareDataDirectory(context.Background(), path, -1, -1); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatal("data directory missing", err)
	}
}
