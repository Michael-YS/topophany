package mihomo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func policyFixture(t *testing.T, configuration string) model.Check {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := map[string]string{"/configs": "configs.json", "/proxies": "proxies.json", "/providers/proxies": "providers.json"}[r.URL.Path]
		if file == "" {
			t.Error("unsafe API read", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/configs" && configuration != "" {
			w.Write([]byte(configuration))
			return
		}
		raw, err := os.ReadFile(filepath.Join("testdata", "v1.19.0", file))
		if err != nil {
			t.Error(err)
			return
		}
		w.Write(raw)
	}))
	t.Cleanup(server.Close)
	x := httpCheck(server.URL)
	x.Kind = "mihomo"
	x.Adapter = model.AdapterOptions{ExpectedMode: "rule", RootProxy: "daily", ExpectedProviders: map[string][]string{"pool": {"node-a", "node-b"}}}
	return x
}
func TestMihomoTUNAndComparisonPolicies(t *testing.T) {
	x := policyFixture(t, "")
	o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "policy" || o.Code != "provider_membership_mismatch" || !strings.Contains(o.Facts["provider_comparison"], "order_changed:true") {
		t.Fatal(o)
	}
	tun := true
	x.Adapter.ExpectedTUN = &tun
	x.Adapter.ProviderMatchModes = map[string]string{"pool": "set"}
	o = (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Connectivity || o.Evidence != "reported" {
		t.Fatal(o)
	}
	raw, _ := json.Marshal(o)
	if strings.Contains(string(raw), "never-export") {
		t.Fatal("fixture secret exported")
	}
	tun = false
	o = (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "policy" || o.Code != "tun_mismatch" {
		t.Fatal(o)
	}
}
func TestMihomoMissingTUNIsUnknown(t *testing.T) {
	x := policyFixture(t, `{"mode":"rule"}`)
	x.Adapter.ExpectedProviders = nil
	value := false
	x.Adapter.ExpectedTUN = &value
	o := (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "unknown" || o.Code != "tun_state_unavailable" || o.Facts["tun_enabled"] != "unknown" {
		t.Fatal(o)
	}
}
