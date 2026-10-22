package beszel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// These are synthetic credentials accepted by the adapter's header contract,
// not a claim that PocketBase issues or accepts numeric/timestamp auth tokens.
func TestBeszelCredentialShapedLikeReportDataCannotEscape(t *testing.T) {
	for _, tc := range []struct{ name, token, stats, created string }{
		{"integer", "9007199254740993", `{"mem":[9007199254740993,1,0,0]}`, testCreated},
		{"normalized-timestamp", "2026-10-21T12:34:56.789Z", `{"cpu":12.5}`, testCreated},
		{"closed-basis", "beszel_agent_percent", `{"cpu":12.5}`, testCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := resourceBody(tc.stats, tc.created, testRecord)
			r, err := resource(body, testSystem)
			if err != nil || r == nil {
				t.Fatalf("fixture is not a valid report: %v", err)
			}
			if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
				t.Fatal(err)
			}
			f := &fixtureIO{token: tc.token, responses: []source.Response{jsonResponse(body)}}
			rt := source.Runtime{Adapters: map[string]source.Adapter{"beszel": Adapter{}}, NewIO: func(model.Config, model.Source) (source.IO, error) { return f, nil }}
			got, err := rt.Collect(context.Background(), model.Config{}, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
			if err == nil || source.Code(err) != "beszel_credential_reflection" || len(got) != 0 || strings.Contains(err.Error(), tc.token) {
				t.Fatal("credential in an otherwise valid report escaped the runtime boundary")
			}
			f = &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(body)}}
			if clean, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}}); err != nil || len(clean) != 1 {
				t.Fatalf("same valid metric with unrelated credential was refused: %v", err)
			}
		})
	}
}

func TestBeszelLaterCredentialReflectionDiscardsResourceAndStatusPrefix(t *testing.T) {
	const other = "othersystem0001"
	for _, statusSecond := range []bool{false, true} {
		t.Run(map[bool]string{false: "second-object", true: "status-after-resource"}[statusSecond], func(t *testing.T) {
			token := "9007199254740993"
			requests := []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}, {Object: other, Reports: []string{model.ReportTypeResource}}}
			second := []byte(strings.Replace(string(resourceBody(`{"mem":[9007199254740993,1,0,0]}`, testCreated, "otherrecord0001")), testSystem, other, 1))
			if statusSecond {
				token = "down"
				requests = []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource, model.ReportTypeStatus}}}
				second = []byte(`{"id":"validationsys01","status":"down"}`)
			}
			f := &fixtureIO{token: token, responses: []source.Response{jsonResponse(resourceBody(`{"cpu":12.5}`, testCreated, testRecord)), jsonResponse(second)}}
			got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requests)
			if err == nil || source.Code(err) != "beszel_credential_reflection" || len(got) != 0 || len(f.gets) != 2 || f.credentials != 1 || strings.Contains(err.Error(), token) {
				t.Fatal("later credential reflection leaked an earlier successful report")
			}
		})
	}
}

func TestBeszelUnmappedCredentialTextStillAllowsSafeMetrics(t *testing.T) {
	const token = "synthetic-ignored-metadata-key"
	stats, err := json.Marshal(map[string]any{"cpu": 12.5, "message": token})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixtureIO{token: token, responses: []source.Response{jsonResponse(resourceBody(string(stats), testCreated, testRecord))}}
	got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
	if err != nil || len(got) != 1 {
		t.Fatalf("ignored text blocked safe metrics: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), token) {
		t.Fatal("unmapped credential text escaped")
	}
}
