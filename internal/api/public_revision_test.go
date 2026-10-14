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

// Viewers see public revisions and contracts only: digests over private
// fields would permit offline guesses at them. Executors and storage keep
// the private contract, so a private-only change still invalidates old
// samples, while a change that measures nothing new does not.
func TestViewerContractsDoNotExposePrivateExecutionIdentity(t *testing.T) {
	original, _, b := fixture(t)
	c := original.Config
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Private": "low-entropy-secret-a"}
	x := c.Checks[0]
	private, public := config.Contract(c, x), config.PublicContracts(c)[x.ID]
	revision, publicRevision := config.Revision(c), config.PublicRevision(c)
	if private == public || revision == publicRevision {
		t.Fatal("fixture does not distinguish private and public digests")
	}
	st, err := store.Open(t.TempDir(), store.Options{Revision: revision, Contracts: config.Contracts(c), Now: original.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(c, "revision-test", st, original.Auth)
	srv.Now = original.Now
	h := srv.Handler()
	b.Revision = revision
	for i := range b.Observations {
		b.Observations[i].Contract = private
		b.Observations[i].SampledAt = srv.Now().Add(-time.Second)
	}
	ack := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	if ack.Code != 202 || !strings.Contains(ack.Body.String(), revision) {
		t.Fatalf("worker acknowledgement lost its revision: %d %s", ack.Code, ack.Body.String())
	}
	for _, tc := range []struct {
		path    string
		minimum int
	}{
		{"/v1/config", 1},
		{"/v1/history?check=" + x.ID, 3},
		{"/v1/snapshot", 3},
		{"/v1/snapshot?observer=home", 3},
		{"/v1/snapshot", 3}, // Exercise cached bytes, not only a fresh build.
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := request(h, "GET", tc.path, viewerToken, "", nil)
			if response.Code != 200 {
				t.Fatalf("viewer response: %d %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			if strings.Contains(body, private) || strings.Contains(body, revision) || strings.Contains(body, "low-entropy-secret-a") {
				t.Fatal("private identity or private field escaped to viewer")
			}
			var decoded any
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			count := 0
			var walk func(any)
			walk = func(v any) {
				switch y := v.(type) {
				case map[string]any:
					for key, value := range y {
						switch {
						case key == "revision":
							count++
							if value != publicRevision {
								t.Errorf("revision=%v, want public revision %s", value, publicRevision)
							}
						case key == "contract" && (y["check"] == x.ID || y["id"] == x.ID):
							count++
							if value != public {
								t.Errorf("contract=%v, want public contract %s", value, public)
							}
						}
						walk(value)
					}
				case []any:
					for _, value := range y {
						walk(value)
					}
				}
			}
			walk(decoded)
			if count < tc.minimum {
				t.Fatalf("vacuous privacy assertion: only %d digests, want at least %d", count, tc.minimum)
			}
		})
	}
	rows, _ := st.View()
	if len(rows) != 1 || rows[0].Contract != private {
		t.Fatal("viewer redaction mutated stored evidence")
	}
	if code, _ := srv.ValidateBatch(b, "home"); code != "" {
		t.Fatalf("valid worker contract rejected after viewer reads: %s", code)
	}
	publicBatch := b
	publicBatch.Observations = []model.Observation{b.Observations[0]}
	publicBatch.Observations[0].Contract = public
	if code, status := srv.ValidateBatch(publicBatch, "home"); code != "contract_mismatch" || status != 409 {
		t.Fatal("public contract accepted as private execution identity")
	}
	// Notes measure nothing; a private-only change still invalidates old work.
	srv.Config.Checks[0].Notes = "edited"
	if code, _ := srv.ValidateBatch(b, "home"); code != "" {
		t.Fatalf("a note edit invalidated in-flight samples: %s", code)
	}
	srv.Config.Checks[0].HTTP.RequestHeaders["X-Private"] = "low-entropy-secret-b"
	if config.PublicContracts(srv.Config)[x.ID] != public {
		t.Fatal("private-only change affected the public contract")
	}
	if code, status := srv.ValidateBatch(b, "home"); code != "contract_mismatch" || status != 409 {
		t.Fatal("old worker sample survived a private configuration change")
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
