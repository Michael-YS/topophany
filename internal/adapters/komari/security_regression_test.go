package komari

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

func TestKomariCredentialShapedLikeReportDataCannotEscape(t *testing.T) {
	for _, tc := range []struct {
		name, token, stats string
	}{
		{"timestamp", testUpdated, `"cpu":{"usage":12.5}`},
		{"integer", "9007199254740993", `"ram":{"total":9007199254740993,"used":4503599627370496}`},
		{"closed-basis", "komari_agent_percent", `"cpu":{"usage":12.5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := resourceBody(tc.stats)
			// The attack is a valid report in the absence of a credential;
			// rejecting malformed input alone cannot satisfy this regression.
			if report, err := resource(body, testObject); err != nil || report == nil {
				t.Fatalf("reflection fixture is not a valid resource: %v", err)
			}
			f := &fixtureIO{token: tc.token, responses: []source.Response{jsonResponse(body)}}
			got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject))
			if source.Code(err) != "komari_credential_reflection" || len(got) != 0 || strings.Contains(err.Error(), tc.token) {
				t.Fatal("credential in a valid report field escaped the collection boundary")
			}
		})
	}
}

func TestKomariLaterCredentialReflectionDiscardsSuccessfulPrefix(t *testing.T) {
	const key = "9007199254740993"
	f := &fixtureIO{token: key, responses: []source.Response{
		jsonResponse(resourceBody(`"cpu":{"usage":12.5}`)),
		jsonResponse(recent(sample(otherObject, testUpdated, `"disk":{"total":9007199254740993,"used":0}`))),
	}}
	got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject, otherObject))
	if source.Code(err) != "komari_credential_reflection" || len(got) != 0 || len(f.gets) != 2 {
		t.Fatal("later reflected credential allowed an earlier report to escape")
	}
}

func TestKomariCredentialGuardUsesJSONEscaping(t *testing.T) {
	// Current mappings have only closed basis strings. Guarding the exact
	// serialized form also prevents a future string field from weakening the
	// boundary when JSON escapes quotes, slashes or HTML-significant text.
	const key = `synthetic<key>&"with\escapes`
	r := &model.ExternalReport{Resource: &model.ResourceReport{CPU: &model.CPUReport{Basis: key}}}
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), key) {
		t.Fatal("fixture does not exercise a JSON-escaped credential")
	}
	if err := credentialSafe(r, key); source.Code(err) != "komari_credential_reflection" {
		t.Fatal("JSON escaping hid a credential from the guard")
	}
}
