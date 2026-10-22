package beszel

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// PocketBase field names are case-sensitive. Go's struct decoder must not
// allow a distinct JSON member to overwrite the canonical protocol member.
func TestBeszelCaseAliasesCannotOverrideProtocolFields(t *testing.T) {
	base := resourceBody(`{"cpu":12.5}`, testCreated, testRecord)
	want, err := resource(base, testSystem)
	if err != nil || want == nil {
		t.Fatalf("canonical control response was rejected: %v", err)
	}
	for _, tc := range []struct {
		name, old, replacement string
	}{
		{"record-id", `"id":"validationrec03"`, `"id":"validationrec03","ID":"validationrec04"`},
		{"record-time", `"created":"2026-10-21 12:34:56.789Z"`, `"created":"2026-10-21 12:34:56.789Z","CREATED":"2026-10-22 12:34:56.789Z"`},
		{"resource-values", `"stats":{"cpu":12.5}`, `"stats":{"cpu":12.5},"STATS":{"cpu":99}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(strings.Replace(string(base), tc.old, tc.replacement, 1))
			if string(body) == string(base) {
				t.Fatal("test did not alter the canonical response")
			}
			got, err := resource(body, testSystem)
			// Rejecting the ambiguous response is safe; ignoring a genuinely
			// unknown member is also safe if the canonical data remains intact.
			if err == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("case alias overrode canonical record data: %+v", got)
			}
		})
	}
	for _, tc := range []struct {
		name, old, replacement string
	}{
		{"system-cannot-be-repaired-by-alias", `"system":"validationsys01"`, `"system":"othersystem0001","SYSTEM":"validationsys01"`},
		{"type-cannot-be-repaired-by-alias", `"type":"1m"`, `"type":"10m","TYPE":"1m"`},
		{"page-cannot-be-repaired-by-alias", `"page":1`, `"page":2,"PAGE":1`},
		{"case-only-required-member", `"system":"validationsys01"`, `"SYSTEM":"validationsys01"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(strings.Replace(string(base), tc.old, tc.replacement, 1))
			if string(body) == string(base) {
				t.Fatal("test did not alter the canonical response")
			}
			if got, err := resource(body, testSystem); err == nil || got != nil {
				t.Fatalf("noncanonical metadata admitted: %+v, %v", got, err)
			}
		})
	}
	got, err := status([]byte(`{"id":"validationsys01","status":"down","STATUS":"up"}`), testSystem)
	if err == nil && (got.Status == nil || got.Status.Status != "down") {
		t.Fatalf("case alias overrode canonical status: %+v", got)
	}
	// Do not solve struct-name aliasing by case-folding every JSON key:
	// case-sensitive device names in efs are distinct upstream identities.
	r, _, err := decodeStats(json.RawMessage(`{"cpu":0,"efs":{"Data":{"d":10,"du":1},"data":{"d":10,"du":2}}}`))
	if err != nil || r == nil || len(r.Disks) != 2 || r.Disks[0].ID == r.Disks[1].ID {
		t.Fatalf("distinct case-sensitive filesystem names were merged: %+v, %v", r, err)
	}
}

func TestBeszelUnpairedUnicodeCannotChangeFilesystemIdentity(t *testing.T) {
	for _, key := range []string{`\ud800`, `\udc00`, `\ud800x`, `\ud800\u0041`, `\udc00\ud800`} {
		t.Run(key, func(t *testing.T) {
			body := []byte(`{"cpu":0,"efs":{"` + key + `":{"d":10,"du":1}}}`)
			if r, _, err := decodeStats(body); err == nil || r != nil {
				t.Fatal("unpaired UTF-16 escape was normalized into a different filesystem identity")
			}
		})
	}
	// A proper surrogate pair and its literal UTF-8 spelling identify the
	// same device. A literal backslash followed by 'ud800' is ordinary text.
	escaped, ef, err := decodeStats(json.RawMessage(`{"cpu":0,"efs":{"\ud83d\ude00":{"d":10,"du":1}}}`))
	if err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
	literal, lf, err := decodeStats(json.RawMessage(`{"cpu":0,"efs":{"😀":{"d":10,"du":1}}}`))
	if err != nil || !reflect.DeepEqual(escaped, literal) || !reflect.DeepEqual(ef, lf) {
		t.Fatalf("equivalent Unicode spellings changed identity: %v", err)
	}
	for _, body := range []string{
		`{"cpu":0,"efs":{"\\ud800":{"d":10,"du":1}}}`,
		`{"cpu":0,"efs":{"�":{"d":10,"du":1}}}`,
		`{"cpu":0,"efs":{"\ufffd":{"d":10,"du":1}}}`,
	} {
		if r, _, err := decodeStats(json.RawMessage(body)); err != nil || r == nil || len(r.Disks) != 1 {
			t.Fatalf("valid Unicode device name rejected: %v", err)
		}
	}
}

func TestBeszelCredentialFragmentsCannotBecomeSampleIDs(t *testing.T) {
	const fragment = "abcdef012345678"
	// The signature component deliberately contains a legal PocketBase record
	// ID. Closed shape alone does not make this peer-controlled string safe.
	const token = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbm9uIn0.abcdef012345678ABCDEFGHIJKLMNopqrstuvwxyz"
	f := &fixtureIO{token: token, responses: []source.Response{jsonResponse(resourceBody(`{"cpu":12.5}`, testCreated, fragment))}}
	got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
	if err == nil || len(got) != 0 {
		t.Fatal("a credential fragment was admitted as a resource sample ID")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), fragment) {
		t.Fatal("credential fragment escaped through the refusal diagnostic")
	}
	// A few coincidentally shared characters must not reject an unrelated ID.
	f = &fixtureIO{token: "eyJhbGciOiJIUzI1NiJ9.valid.signature", responses: []source.Response{jsonResponse(resourceBody(`{"cpu":12.5}`, testCreated, testRecord))}}
	got, err = (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
	if err != nil || len(got) != 1 || got[0].Report.SampleID != testRecord {
		t.Fatalf("unrelated record ID was suppressed: %v", err)
	}
}

func TestBeszelPlaintextLoopbackPolicyRejectsRoutingAliases(t *testing.T) {
	for _, target := range []string{
		"http://localhost", "http://2130706433", "http://127.1", "http://0x7f000001",
		"http://127.000.000.001", "http://127.0.0.1.evil.example", "http://[::]",
		"http://[::1%25lo0]", "http://[::ffff:192.0.2.1]",
		"https://beszel.example.test/escaped%61", "https://beszel.example.test/a//b",
		"https://beszel.example.test/a%2f..%2fb", "https://beszel.example.test?",
	} {
		t.Run(target, func(t *testing.T) {
			s := testSource()
			s.Target = target
			if err := (Adapter{}).Validate(s); err == nil {
				t.Fatal("unsafe or ambiguous transport route admitted")
			}
		})
	}
	for _, target := range []string{"http://127.0.0.1", "http://[::1]", "http://[::ffff:127.0.0.1]"} {
		s := testSource()
		s.Target = target
		if err := (Adapter{}).Validate(s); err != nil {
			t.Fatalf("literal loopback rejected: %s: %v", target, err)
		}
		s.ResolveIP = "192.0.2.1"
		if err := (Adapter{}).Validate(s); err == nil {
			t.Fatal("loopback URL escaped through a remote resolve override")
		}
	}
}
