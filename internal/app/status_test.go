package app

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestStatusNeverClearsAnInconclusiveActiveCheck(t *testing.T) {
	for _, kind := range []string{"error", "unknown", "stale", "never", "paused", "maintenance"} {
		t.Run(kind, func(t *testing.T) {
			c := DemoConfig()
			x := c.Checks[0]
			x.ID = "independent"
			x.Path = ""
			x.Service = ""
			x.Endpoint = ""
			x.Target = "http://127.0.0.1:7447/healthz"
			x.Disabled = kind == "paused"
			x.Maintenance = kind == "maintenance"
			c.Checks = append(c.Checks, x)
			st, e := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c)})
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			now := time.Now().UTC()
			rows := []model.Observation{testkit.Observation(c, "api-health", 1, now, "ok")}
			if kind != "never" {
				v := testkit.Observation(c, x.ID, 2, now, "ok")
				if kind == "stale" {
					v.SampledAt = now.Add(-time.Hour)
				}
				if kind == "error" || kind == "unknown" {
					v.Outcome = kind
				}
				rows = append(rows, v)
			}
			if e = st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: rows}); e != nil {
				t.Fatal(e)
			}
			token := strings.Repeat("v", 64)
			srv := httptest.NewServer(api.New(c, "remote", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(token), Role: "viewer"}}}).Handler())
			defer srv.Close()
			out, e := at(now, "status", "--json", "--credentials", filepath.Join(t.TempDir(), "missing.json"), "--server", srv.URL, "--project", c.Project, "--credential-file", credentialFile(t, token))
			var got statusJSON
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err, out)
			}
			inactive := kind == "paused" || kind == "maintenance"
			if inactive {
				if e != nil || got.Status != "clear" {
					t.Fatal(e, out)
				}
				return
			}
			if exitCode(e) != ExitNoEvidence || got.Status != "no-evidence" || got.Verdict.State != "incomplete" {
				t.Fatal(e, out)
			}
			found := false
			for _, r := range got.Attention {
				found = found || r.Kind == "check" && r.ID == x.ID
			}
			if !found {
				t.Fatal("inconclusive check hidden", out)
			}
		})
	}
}
