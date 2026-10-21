package source_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

type fakeIO struct{ pages int }

func (f *fakeIO) Credential(context.Context, model.Source) (string, error) { return "", nil }
func (f *fakeIO) Get(context.Context, model.Source, string, map[string]string, int64) (source.Response, error) {
	return source.Response{Status: 200}, nil
}
func (f *fakeIO) Pages() int { return f.pages }

type fakeAdapter struct {
	readings []source.Reading
	err      error
	pages    int
	validErr error
	requests []source.Request
}

func (a *fakeAdapter) Validate(model.Source) error { return a.validErr }
func (a *fakeAdapter) Collect(_ context.Context, _ source.IO, _ model.Source, requests []source.Request) ([]source.Reading, error) {
	a.requests = append([]source.Request(nil), requests...)
	return a.readings, a.err
}

func sourceConfig() model.Config {
	c := testkit.Config()
	c.Sources = []model.Source{{ID: "probe-api", Adapter: "synthetic-list", Observer: "home", Target: "https://probe.invalid/v1/objects", IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 2, MaxObjects: 10, MaxReportBytes: 16 << 10}}
	c.Checks = []model.Check{{ID: "status", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "svc", Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 60}}}
	config.Defaults(&c)
	return c
}

func statusReading(object string) source.Reading {
	return source.Reading{Object: object, Report: model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: object, SampleID: "s-" + object, ObservedAt: testTime(), TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: "up"}}}
}

func testTime() time.Time { return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC) }

func statusRequests(objects ...string) []source.Request {
	out := make([]source.Request, 0, len(objects))
	for _, object := range objects {
		out = append(out, source.Request{Object: object, Reports: []string{model.ReportTypeStatus}})
	}
	return out
}

func TestRuntimeValidateRefusesUnwiredAdapterAndInvalidSource(t *testing.T) {
	c := sourceConfig()
	rt := source.Runtime{}
	if err := rt.Validate(c); err == nil {
		t.Fatal("unwired adapter accepted")
	}
	bad := &fakeAdapter{validErr: errors.New("bad version")}
	rt = source.Runtime{Adapters: map[string]source.Adapter{"synthetic-list": bad}, NewIO: func(model.Config, model.Source) (source.IO, error) { return &fakeIO{}, nil }}
	if err := rt.Validate(c); err == nil {
		t.Fatal("adapter validation error accepted")
	}
	bad.validErr = nil
	if err := rt.Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCollectStampsObjectNormalizesAndIgnoresUnboundObjects(t *testing.T) {
	c := sourceConfig()
	src, _ := config.Source(c, "probe-api")
	a := &fakeAdapter{readings: []source.Reading{statusReading("svc"), statusReading("not-bound")}}
	rt := source.Runtime{Adapters: map[string]source.Adapter{"synthetic-list": a}, NewIO: func(model.Config, model.Source) (source.IO, error) { return &fakeIO{}, nil }}
	readings, err := rt.Collect(context.Background(), c, src, statusRequests("svc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 1 || readings[0].Object != "svc" || readings[0].Report.Object != "svc" {
		t.Fatalf("readings = %+v", readings)
	}
}

func TestRuntimeRejectsForeignPayloadFieldWithoutPanic(t *testing.T) {
	c := sourceConfig()
	src, _ := config.Source(c, "probe-api")
	reading := statusReading("svc")
	reading.Report.Fields = map[string]string{"disk.root.percent": model.FieldMissing}
	a := &fakeAdapter{readings: []source.Reading{reading}}
	rt := source.Runtime{Adapters: map[string]source.Adapter{"synthetic-list": a}, NewIO: func(model.Config, model.Source) (source.IO, error) { return &fakeIO{}, nil }}
	if _, err := rt.Collect(context.Background(), c, src, statusRequests("svc")); err == nil || source.Code(err) != "unknown_field" {
		t.Fatalf("foreign status field accepted or lost its code: %v", err)
	}
}

func TestRuntimeCollectRefusesDuplicateBudgetAndPages(t *testing.T) {
	c := sourceConfig()
	src, _ := config.Source(c, "probe-api")
	for name, tc := range map[string]struct {
		readings []source.Reading
		io       source.IO
		err      error
	}{
		"duplicate":        {[]source.Reading{statusReading("svc"), statusReading("svc")}, &fakeIO{}, nil},
		"too many pages":   {[]source.Reading{statusReading("svc")}, &fakeIO{pages: 3}, nil},
		"too many objects": {[]source.Reading{statusReading("svc"), statusReading("svc2"), statusReading("svc3"), statusReading("svc4"), statusReading("svc5"), statusReading("svc6"), statusReading("svc7"), statusReading("svc8"), statusReading("svc9"), statusReading("svc10"), statusReading("svc11")}, &fakeIO{}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			a := &fakeAdapter{readings: tc.readings}
			rt := source.Runtime{Adapters: map[string]source.Adapter{"synthetic-list": a}, NewIO: func(model.Config, model.Source) (source.IO, error) { return tc.io, nil }}
			_, err := rt.Collect(context.Background(), c, src, statusRequests("svc"))
			if err == nil {
				t.Fatal("invalid runtime result accepted")
			}
		})
	}
	// The duplicate/budget cases are checked before filtering to the single
	// wanted object, so a source cannot hide a malformed whole response.
}

func TestRuntimeErrorCodeIsBounded(t *testing.T) {
	e := source.NewError("auth_failed", "credential rejected")
	if source.Code(e) != "auth_failed" {
		t.Fatal(source.Code(e))
	}
	if source.Code(errors.New("raw secret in error")) != "source_collection_failed" {
		t.Fatal("unbounded error code")
	}
	if source.Code(source.NewError("bad/code", "")) != "source_error" {
		t.Fatal("unsafe code accepted")
	}
}

func TestRuntimeBuildsFreshIOSessionPerCollection(t *testing.T) {
	c := sourceConfig()
	src, _ := config.Source(c, "probe-api")
	var sessions int
	a := &fakeAdapter{readings: []source.Reading{statusReading("svc")}}
	rt := source.Runtime{Adapters: map[string]source.Adapter{"synthetic-list": a}, NewIO: func(model.Config, model.Source) (source.IO, error) {
		sessions++
		return &fakeIO{}, nil
	}}
	for i := 0; i < 2; i++ {
		if _, err := rt.Collect(context.Background(), c, src, statusRequests("svc")); err != nil {
			t.Fatal(err)
		}
	}
	if sessions != 2 {
		t.Fatalf("IO sessions = %d; a source read must not reuse another session", sessions)
	}
}

func resourceReading(object string) source.Reading {
	percent := 25.0
	return source.Reading{Object: object, Report: model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: object, SampleID: "r-" + object, ObservedAt: testTime(), TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}}}}
}

func TestRuntimeCollectsResourceAndStatusForOneObject(t *testing.T) {
	c := sourceConfig()
	src, _ := config.Source(c, "probe-api")
	a := &fakeAdapter{readings: []source.Reading{resourceReading("svc"), statusReading("svc")}}
	rt := source.Runtime{Adapters: map[string]source.Adapter{"synthetic-list": a}, NewIO: func(model.Config, model.Source) (source.IO, error) { return &fakeIO{}, nil }}
	readings, err := rt.Collect(context.Background(), c, src, []source.Request{{Object: "svc", Reports: []string{model.ReportTypeResource, model.ReportTypeStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 2 || readings[0].Report.Resource == nil || readings[1].Report.Status == nil {
		t.Fatalf("dual-type readings = %+v", readings)
	}
	if len(a.requests) != 1 || !a.requests[0].Wants(model.ReportTypeResource) || !a.requests[0].Wants(model.ReportTypeStatus) {
		t.Fatalf("adapter request = %+v", a.requests)
	}
	// Omitting a requested type is legal at the runtime boundary; the runner
	// turns that binding into a missing-report attempt.
	a.readings = []source.Reading{statusReading("svc")}
	readings, err = rt.Collect(context.Background(), c, src, []source.Request{{Object: "svc", Reports: []string{model.ReportTypeResource, model.ReportTypeStatus}}})
	if err != nil || len(readings) != 1 || readings[0].Report.Status == nil {
		t.Fatalf("partial result = %+v %v", readings, err)
	}
}

func TestRuntimeRejectsAmbiguousReportRequestsAndResults(t *testing.T) {
	c := sourceConfig()
	src, _ := config.Source(c, "probe-api")
	both := statusReading("svc")
	both.Report.Resource = &model.ResourceReport{CPU: &model.CPUReport{}}
	cases := map[string]struct {
		requests []source.Request
		readings []source.Reading
		code     string
	}{
		"duplicate object request": {[]source.Request{{Object: "svc", Reports: []string{model.ReportTypeStatus}}, {Object: "svc", Reports: []string{model.ReportTypeResource}}}, nil, "duplicate_object_request"},
		"duplicate type request":   {[]source.Request{{Object: "svc", Reports: []string{model.ReportTypeStatus, model.ReportTypeStatus}}}, nil, "duplicate_report_request"},
		"unsupported type":         {[]source.Request{{Object: "svc", Reports: []string{"metric"}}}, nil, "unsupported_report_type"},
		"too many objects":         {[]source.Request{{Object: "svc", Reports: []string{model.ReportTypeStatus}}, {Object: "svc2", Reports: []string{model.ReportTypeStatus}}}, nil, "object_budget"},
		"duplicate result":         {[]source.Request{{Object: "svc", Reports: []string{model.ReportTypeStatus}}}, []source.Reading{statusReading("svc"), statusReading("svc")}, "duplicate_report"},
		"unexpected result type":   {[]source.Request{{Object: "svc", Reports: []string{model.ReportTypeStatus}}}, []source.Reading{resourceReading("svc")}, "unexpected_report_type"},
		"double payload":           {[]source.Request{{Object: "svc", Reports: []string{model.ReportTypeResource, model.ReportTypeStatus}}}, []source.Reading{both}, "unsupported_report_type"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srcBudget := src
			if name == "too many objects" {
				srcBudget.MaxObjects = 1
			}
			a := &fakeAdapter{readings: tc.readings}
			rt := source.Runtime{Adapters: map[string]source.Adapter{"synthetic-list": a}, NewIO: func(model.Config, model.Source) (source.IO, error) { return &fakeIO{}, nil }}
			_, err := rt.Collect(context.Background(), c, srcBudget, tc.requests)
			if err == nil || source.Code(err) != tc.code {
				t.Fatalf("code = %q, want %q (%v)", source.Code(err), tc.code, err)
			}
		})
	}
}
