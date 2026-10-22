package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBeszelValidMetricCredentialReflectionCannotReachCollector(t *testing.T) {
	for _, kind := range []string{"timestamp", "integer"} {
		t.Run(kind, func(t *testing.T) {
			at := time.Now().UTC().Truncate(time.Millisecond).Add(-45 * time.Second)
			key, stats := at.Format(time.RFC3339Nano), `{"cpu":12.5}`
			if kind == "integer" {
				key, stats = "9007199254740993", `{"mem":[9007199254740993,1,0,0]}`
			}
			t.Setenv("TOPO_BESZEL_INTEGRATION_TOKEN", key)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != beszelPrefix+"/api/collections/system_stats/records" || r.Header.Get("Authorization") != key {
					t.Error("reflection fixture received an unexpected request")
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[{"id":%q,"system":%q,"type":"1m","created":%q,"stats":%s}]}`, beszelRecordID, beszelSystemID, at.Format("2006-01-02 15:04:05.000Z"), stats)
			}))
			defer server.Close()
			ch := newBeszelChain(t, beszelIntegrationConfig(t, server.URL))
			ctx := context.Background()
			ch.once(ctx)
			ch.deliver(ctx)
			observations := 0
			for _, batch := range ch.batches {
				for _, o := range batch.Observations {
					observations++
					if o.Outcome != "error" || o.Code != "beszel_credential_reflection" || o.Evidence != "reported" || o.Connectivity || o.External == nil || o.External.Contact || o.External.Report != nil {
						t.Fatal("reflected credential became report, contact or target evidence")
					}
				}
			}
			if observations != 3 {
				t.Fatalf("observations = %d, want all 3 bindings", observations)
			}
			state := ch.st.ExternalView()
			if state.Sources["beszel-hub"].LastCode != "beszel_credential_reflection" || !state.Sources["beszel-hub"].LastContact.IsZero() {
				t.Fatal("collector refreshed contact from reflected credentials")
			}
			for _, binding := range state.Bindings {
				if binding.Last != nil || binding.ReportSamples != 0 || binding.ConfirmedDown {
					t.Fatal("collector retained unsafe report or outage evidence")
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
