package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKomariValidMetricCredentialReflectionCannotReachCollector(t *testing.T) {
	for _, kind := range []string{"timestamp", "integer"} {
		t.Run(kind, func(t *testing.T) {
			at := time.Now().UTC().Add(-45 * time.Second)
			key, stats := at.Format(time.RFC3339Nano), komariStats
			if kind == "integer" {
				key = "9007199254740993"
				stats = `"cpu":{"usage":12.5},"ram":{"total":9007199254740993,"used":4503599627370496}`
			}
			t.Setenv("TOPO_KOMARI_INTEGRATION_TOKEN", key)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != komariPrefix+"/api/recent/"+komariObject || r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("reflection fixture received an unexpected request")
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []json.RawMessage{komariSample(at, stats)}}); err != nil {
					t.Error("reflection fixture could not write its response")
				}
			}))
			defer server.Close()
			ch := newKomariChain(t, komariIntegrationConfig(t, server.URL))
			ctx := context.Background()
			ch.once(ctx)
			ch.deliver(ctx)
			if len(ch.batches) == 0 {
				t.Fatal("reflection did not exercise real delivery")
			}
			for _, batch := range ch.batches {
				for _, o := range batch.Observations {
					if o.Outcome != "error" || o.Evidence != "reported" || o.Connectivity || o.Code != "komari_credential_reflection" || o.External == nil || o.External.Contact || o.External.Report != nil {
						t.Fatal("reflected credential produced resource or target evidence")
					}
				}
			}
			state := ch.st.ExternalView()
			if state.Sources["komari-server"].LastCode != "komari_credential_reflection" || !state.Sources["komari-server"].LastContact.IsZero() {
				t.Fatal("collector accepted source contact from a reflected credential")
			}
			for _, b := range state.Bindings {
				if b.Last != nil || b.ReportSamples != 0 || b.ConfirmedDown {
					t.Fatal("collector retained reflected data or confirmed target failure")
				}
			}
			for _, value := range []any{ch.batches, state, ch.snapshot(time.Now().UTC().Add(time.Second))} {
				raw, err := json.Marshal(value)
				if err != nil || strings.Contains(string(raw), key) {
					t.Fatal("credential reached durable delivery or public output")
				}
			}
		})
	}
}
