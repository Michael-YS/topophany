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

// Cloudflare stores a record's content in its own canonical form, so a
// declared value is compared by meaning: an address by value, a CNAME target
// without case or the root dot. A real difference is still a mismatch, and the
// content itself is never put in a fact or a detail.
func TestCollectDNSRecordContentIsCompared(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		declared string
		reported string
		matches  bool
	}{
		{"A same", "A", "198.51.100.7", "198.51.100.7", true},
		{"A different", "A", "198.51.100.7", "198.51.100.8", false},
		{"A declared unparseable", "A", "198.51.100.7.", "198.51.100.7", false},
		{"A reported unparseable", "A", "198.51.100.7", "not-an-address", false},
		{"AAAA case", "AAAA", "2001:DB8::1", "2001:db8::1", true},
		{"AAAA expanded", "AAAA", "2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8::1", true},
		{"AAAA different", "AAAA", "2001:db8::1", "2001:db8::2", false},
		{"AAAA declared unparseable", "AAAA", "2001:db8::g", "2001:db8::1", false},
		{"AAAA reported unparseable", "AAAA", "2001:db8::1", "", false},
		{"CNAME case and dot", "CNAME", "Example.COM.", "example.com", true},
		{"CNAME dot on reported", "CNAME", "example.com", "example.com.", true},
		{"CNAME identical", "CNAME", "origin.example.net", "origin.example.net", true},
		{"CNAME different", "CNAME", "origin.example.net", "origin.example.org", false},
		{"CNAME extra trailing dot", "CNAME", "example.com..", "example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := dnsOptions()
			options.RecordType = tc.kind
			options.ExpectedContent = tc.declared
			body, _ := json.Marshal(map[string]any{"success": true, "result": []map[string]any{{"content": tc.reported, "proxied": true, "ttl": 300}}})
			o := observation()
			if err := collectDNSRecord(body, options, o); err != nil {
				t.Fatal(err)
			}
			wantMatches, wantCode, wantOutcome := "true", "", "ok"
			if !tc.matches {
				wantMatches, wantCode, wantOutcome = "false", "record_content_mismatch", "policy"
			}
			if o.Facts["record_content_matches"] != wantMatches || o.Code != wantCode || o.Outcome != wantOutcome {
				t.Fatalf("facts=%+v outcome=%s code=%q", o.Facts, o.Outcome, o.Code)
			}
			raw, _ := json.Marshal(o)
			for _, content := range []string{tc.declared, tc.reported} {
				if len(content) >= 8 && strings.Contains(string(raw), content) {
					t.Fatalf("record content %q reached the observation: %s", content, raw)
				}
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
