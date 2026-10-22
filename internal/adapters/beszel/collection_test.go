package beszel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

func TestLatestResourceReadSkipsHistoryCount(t *testing.T) {
	// PocketBase returns -1 for both totals when skipTotal is enabled,
	// including for an empty result. No historical count is needed to read
	// one latest sample, and the count query is not atomic with the item read.
	for _, tc := range []struct {
		name  string
		empty bool
	}{{"sample", false}, {"empty", true}} {
		t.Run(tc.name, func(t *testing.T) {
			body := resourceBody(`{"cpu":12.5}`, testCreated, testRecord)
			var page map[string]any
			if err := json.Unmarshal(body, &page); err != nil {
				t.Fatal(err)
			}
			page["totalItems"], page["totalPages"] = -1, -1
			if tc.empty {
				page["items"] = []any{}
			}
			body, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(body)}}
			got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
			if err != nil {
				t.Fatalf("count-free latest read failed: %v", err)
			}
			if len(f.gets) != 1 {
				t.Fatalf("latest read made %d requests, want 1", len(f.gets))
			}
			u, err := url.Parse(f.gets[0].url)
			if err != nil || u.Query().Get("skipTotal") != "true" {
				t.Fatal("latest read did not disable PocketBase's historical count query")
			}
			if tc.empty {
				if len(got) != 0 {
					t.Fatal("empty count-free list fabricated a sample")
				}
			} else if len(got) != 1 || got[0].Report.SampleID != testRecord || got[0].Report.Resource == nil || got[0].Report.Resource.CPU == nil {
				t.Fatalf("latest sample was lost: %+v", got)
			} else {
				wantValue(t, "cpu percent", got[0].Report.Resource.CPU.Percent, 12.5)
			}
		})
	}
}

func TestMultipleSystemsKeepBindingsAndFailAtomically(t *testing.T) {
	const otherSystem = "othersystem0001"
	const otherRecord = "otherrecord0001"
	requests := []source.Request{
		{Object: testSystem, Reports: []string{model.ReportTypeStatus, model.ReportTypeResource}},
		{Object: otherSystem, Reports: []string{model.ReportTypeResource, model.ReportTypeStatus}},
	}
	s := testSource()
	s.MaxPages, s.MaxObjects = 4, 2
	first := resourceBody(`{"cpu":12.5}`, testCreated, testRecord)
	second := []byte(strings.Replace(string(resourceBody(`{"cpu":75}`, testCreated, otherRecord)), testSystem, otherSystem, 1))
	for _, tc := range []struct {
		name     string
		failLast bool
	}{{"success", false}, {"last-system-fails", true}} {
		t.Run(tc.name, func(t *testing.T) {
			responses := []source.Response{
				jsonResponse(first), jsonResponse([]byte(`{"id":"validationsys01","status":"up"}`)),
				jsonResponse(second), jsonResponse([]byte(`{"id":"othersystem0001","status":"down"}`)),
			}
			if tc.failLast {
				responses[3] = source.Response{Status: http.StatusForbidden}
			}
			f := &fixtureIO{token: testToken, responses: responses}
			got, err := (Adapter{}).Collect(context.Background(), f, s, requests)
			if f.credentials != 1 || len(f.gets) != 4 || f.pages != 4 {
				t.Fatalf("unexpected collection IO: credentials=%d reads=%d pages=%d", f.credentials, len(f.gets), f.pages)
			}
			if tc.failLast {
				if source.Code(err) != "beszel_auth" || len(got) != 0 {
					t.Fatalf("later system failure leaked earlier reports: %+v, %v", got, err)
				}
				return
			}
			if err != nil || len(got) != 4 {
				t.Fatalf("multi-system collection = %+v, %v", got, err)
			}
			for i, want := range []struct{ object, sample, status string }{
				{testSystem, testRecord, "up"}, {otherSystem, otherRecord, "down"},
			} {
				resource, status := got[2*i], got[2*i+1]
				if resource.Object != want.object || resource.Report.Object != want.object || resource.Report.SampleID != want.sample || resource.Report.Resource == nil || resource.Report.Resource.CPU == nil ||
					status.Object != want.object || status.Report.Object != want.object || status.Report.Status == nil || status.Report.Status.Status != want.status {
					t.Fatalf("system bindings crossed: resource=%+v status=%+v", resource, status)
				}
				wantValue(t, "cpu percent", resource.Report.Resource.CPU.Percent, []float64{12.5, 75}[i])
				resourceURL, _ := url.Parse(f.gets[2*i].url)
				statusURL, _ := url.Parse(f.gets[2*i+1].url)
				if resourceURL.Query().Get("filter") != `system = "`+want.object+`" && type = "1m"` || statusURL.Path != "/hub/api/collections/systems/records/"+want.object {
					t.Fatalf("request targeted the wrong system: %s, %s", resourceURL, statusURL)
				}
			}
		})
	}
}
