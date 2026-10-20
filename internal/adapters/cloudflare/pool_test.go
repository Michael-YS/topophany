package cloudflare

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCollectPoolHealthFixture(t *testing.T) {
	o := observation()
	if err := collectPool(fixture(t, "pool-health.json"), poolOptions(), o); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"regions_reporting": "3",
		"origins_total":     "4",
		"origins_healthy":   "3",
		"pool_healthy":      "false",
	} {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
		}
	}
	if o.Outcome != "ok" {
		t.Fatalf("outcome=%s", o.Outcome)
	}
	raw, _ := json.Marshal(o.Facts)
	// The undeclared origin and the synthetic failure text never become facts.
	for _, forbidden := range []string{"198.51.100.99", "synthetic placeholder", "44444444444444444444444444444444"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("%q reached the facts: %s", forbidden, raw)
		}
	}
}

func TestCollectPoolHealthDeclaredOrigins(t *testing.T) {
	cases := []struct {
		name     string
		declared []string
		facts    map[string]string
		code     string
	}{
		{
			name:     "healthy declared origins",
			declared: []string{"192.0.2.10", "2001:db8::10"},
			facts:    map[string]string{"origin.192.0.2.10": "healthy", "origin.2001:db8::10": "healthy"},
		},
		{
			name:     "one unhealthy origin",
			declared: []string{"192.0.2.10", "192.0.2.11"},
			facts:    map[string]string{"origin.192.0.2.10": "healthy", "origin.192.0.2.11": "unhealthy"},
			code:     "origins_below_expected",
		},
		{
			name:     "missing origin",
			declared: []string{"192.0.2.55"},
			code:     "origins_below_expected",
		},
		{
			name:     "declared name matches case-insensitively",
			declared: []string{"Tunnel.Example.COM"},
			facts:    map[string]string{"origin.Tunnel.Example.COM": "healthy"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := poolOptions()
			options.ExpectedOrigins = tc.declared
			o := observation()
			body := fixture(t, "pool-health.json")
			if tc.name == "declared name matches case-insensitively" {
				body = []byte(`{"success":true,"result":{"pop_health":{"HKG":{"healthy":true,"origins":[{"tunnel.example.com":{"healthy":true}}]}}}}`)
			}
			if err := collectPool(body, options, o); err != nil {
				t.Fatal(err)
			}
			for key, value := range tc.facts {
				if o.Facts[key] != value {
					t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
				}
			}
			if o.Code != tc.code {
				t.Fatalf("code=%q want %q", o.Code, tc.code)
			}
			if tc.code != "" && o.Outcome != "policy" {
				t.Fatalf("outcome=%s", o.Outcome)
			}
		})
	}
}

func TestCollectPoolHealthSingleDocumentedShape(t *testing.T) {
	o := observation()
	options := poolOptions()
	options.ExpectedOrigins = []string{"192.0.2.10"}
	if err := collectPool(fixture(t, "pool-health-single.json"), options, o); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"regions_reporting": "1",
		"origins_total":     "1",
		"origins_healthy":   "1",
		"pool_healthy":      "true",
		"origin.192.0.2.10": "healthy",
	} {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
		}
	}
	if o.Outcome != "ok" {
		t.Fatalf("outcome=%s", o.Outcome)
	}
}

func TestCollectPoolHealthShape(t *testing.T) {
	for _, body := range []string{
		`{"result":{"pop_health":{"healthy":true,"origins":[]}}}`,
		`{"success":true,"result":{}}`,
		`{"success":true,"result":{"pop_health":null}}`,
		`{"success":true,"result":{"pop_health":{}}}`,
		`{"success":true,"result":{"pop_health":"nope"}}`,
		`{"success":true,"result":null}`,
	} {
		if err := collectPool([]byte(body), poolOptions(), observation()); failureCode(err) != "unsupported_response_shape" {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

// A region that omits healthy for an origin does not establish it healthy;
// the counts and the declared verdict say so rather than inventing a value.
func TestCollectPoolHealthMissingOriginHealth(t *testing.T) {
	body := `{"success":true,"result":{"pop_health":{"HKG":{"origins":[{"192.0.2.10":{}}]}}}}`
	options := poolOptions()
	options.ExpectedOrigins = []string{"192.0.2.10"}
	o := observation()
	if err := collectPool([]byte(body), options, o); err != nil {
		t.Fatal(err)
	}
	if o.Facts["origins_healthy"] != "0" || o.Facts["origin.192.0.2.10"] != "unhealthy" || o.Code != "origins_below_expected" {
		t.Fatalf("facts=%+v code=%s", o.Facts, o.Code)
	}
	if _, ok := o.Facts["pool_healthy"]; ok {
		t.Fatal("pool_healthy invented without a region verdict")
	}
}

// An absent declared origin is policy, but the adapter must not report an
// unhealthy verdict the platform never gave. A present unhealthy origin keeps
// its unhealthy fact; an absent one has no fact at all.
func TestCollectPoolHealthAbsentOriginIsPolicyWithoutFact(t *testing.T) {
	options := poolOptions()
	options.ExpectedOrigins = []string{"192.0.2.55", "192.0.2.11"}
	o := observation()
	if err := collectPool(fixture(t, "pool-health.json"), options, o); err != nil {
		t.Fatal(err)
	}
	if o.Outcome != "policy" || o.Code != "origins_below_expected" {
		t.Fatalf("observation %+v", o)
	}
	if _, ok := o.Facts["origin.192.0.2.55"]; ok {
		t.Fatalf("absent origin got a verdict: %+v", o.Facts)
	}
	if o.Facts["origin.192.0.2.11"] != "unhealthy" {
		t.Fatalf("unhealthy origin lost its fact: %+v", o.Facts)
	}
}
