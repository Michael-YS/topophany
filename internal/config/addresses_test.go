package config_test

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"reflect"
	"strings"
	"testing"
)

func TestAddressReferencesPreserveSourceAndMeasurementIdentity(t *testing.T) {
	c := testkit.Config()
	original := config.Contracts(c)
	c.Addresses = map[string]string{"cloud": "192.0.2.10"}
	old := c.Endpoints[0].Address
	c.Endpoints[0].Address = strings.ReplaceAll(old, "192.0.2.10", "${cloud}")
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(config.Contracts(c), original) {
		t.Fatal("equivalent address reference reset history")
	}
	raw, _ := json.Marshal(c)
	got, e := config.Decode(strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	if got.Endpoints[0].Address != c.Endpoints[0].Address {
		t.Fatal("decoder flattened references")
	}
	public := config.Public(c)
	if public.Endpoints[0].Address != old || public.Addresses != nil {
		t.Fatal("public topology contains unresolved references", public.Endpoints[0])
	}
	copy := config.Clone(c)
	copy.Addresses["cloud"] = "192.0.2.9"
	if c.Addresses["cloud"] != "192.0.2.10" {
		t.Fatal("shared address map")
	}
	after := config.Contracts(copy)
	var changed int
	for id, contract := range original {
		if after[id] != contract {
			changed++
		}
	}
	expected := 0
	for _, check := range c.Checks {
		if check.Endpoint == c.Endpoints[0].ID {
			expected++
		}
	}
	if changed != expected {
		t.Fatal("unrelated measurement changed", changed)
	}
}
func TestAddressReferencesFailClosed(t *testing.T) {
	for _, value := range []string{"https://example.com/", "user:password@example.com", "${recursive}", "a\nexample.com", ""} {
		c := testkit.Config()
		c.Addresses = map[string]string{"target": value}
		if e := config.Validate(c); e == nil {
			t.Fatal("accepted non-address", value)
		}
	}
	c := testkit.Config()
	c.Endpoints[0].Address = "http://${missing}:8080/"
	if e := config.Validate(c); e == nil {
		t.Fatal("missing reference accepted")
	}
	c.Addresses = map[string]string{"missing": "127.0.0.1"}
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	delete(c.Addresses, "missing")
	if e := config.Validate(c); e == nil {
		t.Fatal("deleted referenced address accepted")
	}
}

func TestReferencesCannotHideInsecureCredentialTransport(t *testing.T) {
	c := testkit.Config()
	c.Addresses = map[string]string{"controller": "127.0.0.1"}
	c.Checks[0].Target = "http://${controller}:9097/?token=private"
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	c.Addresses["controller"] = "192.0.2.9"
	if e := config.Validate(c); e == nil {
		t.Fatal("public plaintext credential transport accepted through a reference")
	}
}
