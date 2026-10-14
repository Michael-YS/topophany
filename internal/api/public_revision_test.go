package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/store"
)

func TestViewerRevisionsDoNotExposePrivateExecutionIdentity(t *testing.T) {
	original, _, b := fixture(t)
	c := original.Config
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Private": "low-entropy-secret-a"}
	private, public := config.Revision(c), config.PublicRevision(c)
	if private == public {
		t.Fatal("fixture does not distinguish private and public revisions")
	}
	st, err := store.Open(t.TempDir(), store.Options{Revision: private, Now: original.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(c, "revision-test", st, original.Auth)
	srv.Now = original.Now
	h := srv.Handler()
	b.Revision = private
	for i := range b.Observations {
		b.Observations[i].Revision = private
		b.Observations[i].SampledAt = srv.Now().Add(-time.Second)
	}
	ack := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	if ack.Code != 202 || !strings.Contains(ack.Body.String(), private) {
		t.Fatalf("worker acknowledgement lost execution identity: %d %s", ack.Code, ack.Body.String())
	}
	for _, tc := range []struct {
		path    string
		minimum int
	}{
		{"/v1/config", 1},
		{"/v1/history?check=" + b.Observations[0].Check, 2},
		{"/v1/snapshot", 2},
		{"/v1/snapshot?observer=home", 2},
		{"/v1/snapshot", 2}, // Exercise cached bytes, not only a fresh build.
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := request(h, "GET", tc.path, viewerToken, "", nil)
			if response.Code != 200 {
				t.Fatalf("viewer response: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), private) || strings.Contains(response.Body.String(), "low-entropy-secret-a") {
				t.Fatal("private identity or private field escaped to viewer")
			}
			var decoded any
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			count := 0
			var walk func(any)
			walk = func(v any) {
				switch x := v.(type) {
				case map[string]any:
					for key, value := range x {
						if key == "revision" {
							count++
							if value != public {
								t.Errorf("revision=%v, want public revision %s", value, public)
							}
						}
						walk(value)
					}
				case []any:
					for _, value := range x {
						walk(value)
					}
				}
			}
			walk(decoded)
			if count < tc.minimum {
				t.Fatalf("vacuous privacy assertion: only %d revisions, want at least %d", count, tc.minimum)
			}
		})
	}
	rows, _ := st.View()
	if len(rows) != 1 || rows[0].Revision != private {
		t.Fatal("viewer redaction mutated stored evidence")
	}
	if code, _ := srv.ValidateBatch(b, "home"); code != "" {
		t.Fatalf("valid worker revision rejected after viewer reads: %s", code)
	}
	publicBatch := b
	publicBatch.Revision = public
	if code, status := srv.ValidateBatch(publicBatch, "home"); code != "revision_mismatch" || status != 409 {
		t.Fatal("public revision accepted as private execution identity")
	}
	// A private-only configuration change must still invalidate old work.
	srv.Config.Checks[0].HTTP.RequestHeaders["X-Private"] = "low-entropy-secret-b"
	if config.PublicRevision(srv.Config) != public {
		t.Fatal("private-only change affected public revision")
	}
	if code, status := srv.ValidateBatch(b, "home"); code != "revision_mismatch" || status != 409 {
		t.Fatal("old worker batch survived a private configuration change")
	}
}

func TestSummaryDoesNotMutatePrivateLastObservation(t *testing.T) {
	srv, _, b := fixture(t)
	srv.Config.Checks[0].HTTP.BearerEnv = "PRIVATE_ENV"
	var snap model.Snapshot
	// Obtain the concrete check-state slice type through JSON without coupling
	// this test to unrelated model struct names.
	encoded := payload(t, map[string]any{
		"revision": config.Revision(srv.Config), "topology": srv.Config,
		"checks": []any{map[string]any{"id": b.Observations[0].Check, "last": b.Observations[0]}},
	})
	if err := json.Unmarshal(encoded, &snap); err != nil {
		t.Fatal(err)
	}
	before := payload(t, snap)
	if _, err := summaryJSON(snap); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(payload(t, snap)) {
		t.Fatal("summary serialization mutated caller-owned evidence")
	}
}
