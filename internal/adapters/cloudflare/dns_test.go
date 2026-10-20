package cloudflare

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCollectDNSRecordFixture(t *testing.T) {
	o := observation()
	if err := collectDNSRecord(fixture(t, "dns-records.json"), dnsOptions(), o); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"record_count":           "1",
		"record_proxied":         "true",
		"record_ttl":             "300",
		"record_content_matches": "not_declared",
	} {
		if o.Facts[key] != value {
			t.Fatalf("%s=%q want %q", key, o.Facts[key], value)
		}
	}
	if o.Outcome != "ok" {
		t.Fatalf("outcome=%s", o.Outcome)
	}
	raw, _ := json.Marshal(o.Facts)
	for _, forbidden := range []string{"198.51.100.7", "dddddddd", "www.example.com", "example.com"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("%q reached the facts: %s", forbidden, raw)
		}
	}
}

func TestCollectDNSRecordPolicies(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		mutate func(*model.CloudflareOptions)
		code   string
		match  string
	}{
		{name: "matching content", body: string(fixture(t, "dns-records.json")), mutate: func(o *model.CloudflareOptions) { o.ExpectedContent = "198.51.100.7" }, match: "true"},
		{name: "mismatched content", body: string(fixture(t, "dns-records.json")), mutate: func(o *model.CloudflareOptions) { o.ExpectedContent = "192.0.2.1" }, code: "record_content_mismatch", match: "false"},
		{name: "expected proxied", body: string(fixture(t, "dns-records.json")), mutate: func(o *model.CloudflareOptions) { o.ExpectedProxied = boolPtr(true) }},
		{name: "proxied unexpectedly", body: string(fixture(t, "dns-records.json")), mutate: func(o *model.CloudflareOptions) { o.ExpectedProxied = boolPtr(false) }, code: "record_proxied_unexpectedly"},
		{name: "missing", body: `{"success":true,"result":[]}`, mutate: func(o *model.CloudflareOptions) { o.ExpectedContent = "198.51.100.7" }, code: "record_missing", match: "false"},
		{name: "not proxied", body: `{"success":true,"result":[{"content":"198.51.100.7","proxied":false,"ttl":300}]}`, mutate: func(o *model.CloudflareOptions) {
			o.ExpectedProxied = boolPtr(true)
		}, code: "record_not_proxied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := dnsOptions()
			tc.mutate(&options)
			o := observation()
			if err := collectDNSRecord([]byte(tc.body), options, o); err != nil {
				t.Fatal(err)
			}
			if o.Code != tc.code {
				t.Fatalf("code=%q want %q", o.Code, tc.code)
			}
			if tc.code != "" && o.Outcome != "policy" {
				t.Fatalf("outcome=%s", o.Outcome)
			}
			if tc.match != "" && o.Facts["record_content_matches"] != tc.match {
				t.Fatalf("matches=%q want %q", o.Facts["record_content_matches"], tc.match)
			}
		})
	}
}

func TestCollectDNSRecordMultipleRecords(t *testing.T) {
	body := `{"success":true,"result":[` +
		`{"content":"198.51.100.7","proxied":true,"ttl":300},` +
		`{"content":"198.51.100.8","proxied":false,"ttl":60}` +
		`]}`
	o := observation()
	if err := collectDNSRecord([]byte(body), dnsOptions(), o); err != nil {
		t.Fatal(err)
	}
	if o.Facts["record_count"] != "2" {
		t.Fatalf("count=%s", o.Facts["record_count"])
	}
	if _, ok := o.Facts["record_proxied"]; ok {
		t.Fatal("mixed proxied values were collapsed")
	}
	if _, ok := o.Facts["record_ttl"]; ok {
		t.Fatal("mixed TTLs were collapsed")
	}
	// Every record must match a declared content; one difference is false.
	options := dnsOptions()
	options.ExpectedContent = "198.51.100.7"
	o = observation()
	if err := collectDNSRecord([]byte(body), options, o); err != nil {
		t.Fatal(err)
	}
	if o.Facts["record_content_matches"] != "false" || o.Code != "record_content_mismatch" {
		t.Fatalf("facts=%+v code=%s", o.Facts, o.Code)
	}
}

func TestCollectDNSRecordMissingProxied(t *testing.T) {
	body := `{"success":true,"result":[{"content":"198.51.100.7","ttl":300}]}`
	o := observation()
	if err := collectDNSRecord([]byte(body), dnsOptions(), o); err != nil {
		t.Fatalf("omitted proxied refused without an assertion: %v", err)
	}
	if _, ok := o.Facts["record_proxied"]; ok {
		t.Fatal("absent proxied became a fact")
	}
	options := dnsOptions()
	options.ExpectedProxied = boolPtr(true)
	if err := collectDNSRecord([]byte(body), options, observation()); failureCode(err) != "unsupported_response_shape" {
		t.Fatalf("err=%v", err)
	}
}

func TestCollectDNSRecordShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{"missing result", `{"success":true}`, "unsupported_response_shape"},
		{"null result", `{"success":true,"result":null}`, "unsupported_response_shape"},
		{"missing success", `{"result":[]}`, "unsupported_response_shape"},
		{"partial page", `{"success":true,"result":[{"content":"198.51.100.7","proxied":true,"ttl":300}],"result_info":{"total_count":2}}`, "unsupported_response_shape"},
		{"complete page", `{"success":true,"result":[{"content":"198.51.100.7","proxied":true,"ttl":300}],"result_info":{"count":1,"total_count":1}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := observation()
			err := collectDNSRecord([]byte(tc.body), dnsOptions(), o)
			if tc.code != "" {
				if failureCode(err) != tc.code {
					t.Fatalf("code=%q err=%v", failureCode(err), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if o.Facts["record_count"] != "1" {
				t.Fatalf("count=%s", o.Facts["record_count"])
			}
		})
	}
	// An explicitly empty list is the platform saying the record is absent:
	// policy, not a malformed response.
	o := observation()
	if err := collectDNSRecord([]byte(`{"success":true,"result":[]}`), dnsOptions(), o); err != nil {
		t.Fatal(err)
	}
	if o.Outcome != "policy" || o.Code != "record_missing" || o.Facts["record_count"] != "0" {
		t.Fatalf("observation %+v", o)
	}
}
