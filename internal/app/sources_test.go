package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcesRealCommandEnvelopesAndSecrets(t *testing.T) {
	const secret = "synthetic-secret-canary-please-never-print"
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "auth-") {
			_, _ = w.Write([]byte(`{"token":"` + secret + `","record":{"id":"uuuuuuuuuuuuuuu","collectionName":"users"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"page":1,"perPage":100,"totalPages":1,"totalItems":1,"items":[{"id":"aaaaaaaaaaaaaaa","name":"Synthetic"}]}`))
	}))
	defer hub.Close()
	root := filepath.Join(t.TempDir(), "direct")
	request := map[string]any{"schema": 1, "request_id": "settings-42", "profile": "desktop", "source": map[string]any{"id": "bs-test", "name": "Synthetic", "adapter": "beszel", "url": hub.URL}, "auth": map[string]any{"mode": "password", "identity": "synthetic@example.invalid", "password": secret}, "selected_objects": []string{"aaaaaaaaaaaaaaa"}, "base_revision": ""}
	run := func(cmd string, want int) map[string]any {
		t.Helper()
		in, _ := json.Marshal(request)
		var out, diagnostic bytes.Buffer
		a := &App{In: bytes.NewReader(in), Out: &out, Err: &diagnostic}
		flag := "--json-stdin"
		if cmd == "list" {
			flag = "--json"
		}
		e := a.Run(context.Background(), []string{"sources", cmd, flag, "--direct-root", root})
		exit := 0
		var ee *ExitError
		if errors.As(e, &ee) {
			exit = ee.Code
		} else if e != nil {
			t.Fatal(e)
		}
		if exit != want {
			t.Fatalf("%s exit=%d want=%d: %s", cmd, exit, want, out.String())
		}
		if strings.Contains(out.String()+diagnostic.String(), secret) {
			t.Fatal("secret in output")
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil || !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			t.Fatal(out.String())
		}
		return result
	}
	result := run("inspect", 0)
	if result["ok"] != true {
		t.Fatal(result)
	}
	if _, e := os.Stat(root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("inspect wrote files", e)
	}
	result = run("save", 0)
	data := result["data"].(map[string]any)
	rev := data["profile_revision"]
	run("list", 0)
	run("save", 1)
	request["base_revision"] = rev
	request["source_id"] = "bs-test"
	run("remove", 0)
	run("list", 0)
	request["schema"] = "wrong"
	run("save", 2)
}
