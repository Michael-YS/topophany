package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func sourceRunnerConfig(t testing.TB, count int, reportType string) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Checks = nil
	c.Sources = []model.Source{{ID: "probe-api", Adapter: "synthetic-list", Observer: "home", Target: "https://probe.invalid/v1/objects", IntervalSeconds: 1, TimeoutSeconds: 2, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 2, MaxObjects: 200, MaxReportBytes: 64 << 10}}
	resource := reportType == model.ReportTypeResource
	if resource {
		for i := 0; i < count; i++ {
			c.Nodes[2].ResourceChecks = append(c.Nodes[2].ResourceChecks, fmt.Sprintf("external-%03d", i))
		}
	}
	for i := 0; i < count; i++ {
		binding := &model.ExternalBinding{Source: "probe-api", Object: fmt.Sprintf("object-%03d", i), Report: reportType, Node: "dmit", MaxAgeSeconds: 60}
		if resource {
			binding.PrimaryDisk = "root"
		}
		c.Checks = append(c.Checks, model.Check{ID: fmt.Sprintf("external-%03d", i), Observer: "home", Kind: "external", External: binding})
	}
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

type fakeSourceRuntime struct {
	calls    atomic.Int32
	readings []source.Reading
	err      error
	started  chan struct{}
	block    bool
	bySource map[string][]source.Reading
	mu       sync.Mutex
	requests map[string][]source.Request
}

func (f *fakeSourceRuntime) Validate(model.Config) error { return nil }
func (f *fakeSourceRuntime) Collect(ctx context.Context, c model.Config, s model.Source, requests []source.Request) ([]source.Reading, error) {
	f.calls.Add(1)
	f.mu.Lock()
	if f.requests == nil {
		f.requests = map[string][]source.Request{}
	}
	copied := make([]source.Request, len(requests))
	for i, request := range requests {
		copied[i] = source.Request{Object: request.Object, Reports: append([]string(nil), request.Reports...)}
	}
	f.requests[s.ID] = append(f.requests[s.ID], copied...)
	f.mu.Unlock()
	if f.started != nil {
		select {
		case <-f.started:
		default:
			close(f.started)
		}
	}
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.bySource != nil {
		if readings, ok := f.bySource[s.ID]; ok {
			return readings, nil
		}
	}
	return f.readings, f.err
}

func statusReadings(c model.Config, at time.Time) []source.Reading {
	out := []source.Reading{}
	for _, x := range c.Checks {
		if x.Kind != "external" || x.External == nil {
			continue
		}
		r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: x.External.Object, SampleID: "sample-" + x.External.Object, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: "up"}}
		out = append(out, source.Reading{Object: x.External.Object, Report: r})
	}
	return out
}

func TestSourceOnceFansOutOneHundredBindingsFromOneRead(t *testing.T) {
	c := sourceRunnerConfig(t, 100, model.ReportTypeStatus)
	now := time.Now().UTC()
	rt := &fakeSourceRuntime{readings: statusReadings(c, now.Add(-time.Minute))}
	var mu sync.Mutex
	batches := [][]model.Observation{}
	s, err := New(c, "home", 1, func(context.Context, model.Config, model.Check) model.Observation {
		t.Fatal("native probe ran for external config")
		return model.Observation{}
	}, func(model.Observation) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s.WithSources(rt, func(obs []model.Observation) error {
		mu.Lock()
		batches = append(batches, append([]model.Observation(nil), obs...))
		mu.Unlock()
		return nil
	}, 4)
	if err := s.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.calls.Load() != 1 {
		t.Fatalf("source reads = %d", rt.calls.Load())
	}
	if len(batches) != 1 || len(batches[0]) != 100 {
		t.Fatalf("fan-out batches = %d %d", len(batches), len(batches[0]))
	}
	seen := map[string]bool{}
	for _, o := range batches[0] {
		if seen[o.ID] || o.External == nil || o.External.Report == nil || !o.External.Contact || o.Evidence != "reported" || o.Connectivity || o.External.Source != "probe-api" || o.External.SourceGeneration == "" {
			t.Fatalf("observation = %+v", o)
		}
		seen[o.ID] = true
	}
	if len(seen) != 100 {
		t.Fatalf("unique observations = %d", len(seen))
	}
}

func TestSourceMissingObjectAndFailureAreAttemptsNotTargetFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime *fakeSourceRuntime
		code    string
		contact bool
		wants   int
	}{
		{"missing object", &fakeSourceRuntime{readings: nil}, "external_object_missing", true, 2},
		{"source error", &fakeSourceRuntime{err: source.NewError("auth_failed", "rejected")}, "auth_failed", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sourceRunnerConfig(t, 2, model.ReportTypeStatus)
			if tc.name == "missing object" {
				readings := statusReadings(c, time.Now().Add(-time.Minute))
				tc.runtime.readings = readings[:1]
			}
			var got []model.Observation
			s, _ := New(c, "home", 1, func(context.Context, model.Config, model.Check) model.Observation { return model.Observation{} }, func(model.Observation) error { return nil })
			s.WithSources(tc.runtime, func(obs []model.Observation) error { got = append(got, obs...); return nil }, 4)
			if err := s.Once(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.wants {
				t.Fatalf("observations = %d", len(got))
			}
			okReports, failed := 0, 0
			for _, o := range got {
				if o.External == nil || o.External.Contact != tc.contact || o.Connectivity {
					t.Fatalf("attempt contact = %+v", o)
				}
				if o.Outcome == "ok" && o.External.Report != nil {
					okReports++
					continue
				}
				if o.Outcome != "error" || o.External.Report != nil || o.Code != tc.code {
					t.Fatalf("attempt = %+v", o)
				}
				failed++
			}
			if tc.name == "missing object" {
				if okReports != 1 || failed != 1 {
					t.Fatalf("missing object fan-out: ok=%d failed=%d", okReports, failed)
				}
			} else if failed != 2 || okReports != 0 {
				t.Fatalf("source failure fan-out: ok=%d failed=%d", okReports, failed)
			}
		})
	}
}

func TestSourceRequiresExplicitRuntimeAndStopsOnSinkFailure(t *testing.T) {
	c := sourceRunnerConfig(t, 1, model.ReportTypeStatus)
	s, _ := New(c, "home", 1, func(context.Context, model.Config, model.Check) model.Observation { return model.Observation{} }, func(model.Observation) error { return nil })
	if err := s.Once(context.Background()); err == nil {
		t.Fatal("external source ran without a runtime")
	}
	rt := &fakeSourceRuntime{readings: statusReadings(c, time.Now().Add(-time.Minute))}
	s.WithSources(rt, func([]model.Observation) error { return errors.New("disk full") }, 4)
	if err := s.Once(context.Background()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("sink failure = %v", err)
	}
}

func TestSourceCancellationRejectsOldResults(t *testing.T) {
	c := sourceRunnerConfig(t, 1, model.ReportTypeStatus)
	rt := &fakeSourceRuntime{block: true, started: make(chan struct{})}
	var sunk atomic.Int32
	s, _ := New(c, "home", 1, func(context.Context, model.Config, model.Check) model.Observation { return model.Observation{} }, func(model.Observation) error {
		sunk.Add(1)
		return nil
	})
	s.WithSources(rt, func([]model.Observation) error { sunk.Add(1); return nil }, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-rt.started:
	case <-time.After(3 * time.Second):
		t.Fatal("source did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop")
	}
	if sunk.Load() != 0 {
		t.Fatalf("canceled source delivered %d results", sunk.Load())
	}
}

func TestSplitPreparedBoundsBytesAndCount(t *testing.T) {
	many := make([]model.Observation, 205)
	for i := range many {
		many[i] = model.Observation{ID: fmt.Sprintf("home.boot.%d", i+1), SampledAt: time.Now()}
	}
	batches, err := splitPrepared(many, 512<<10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 || len(batches[0]) != 100 || len(batches[1]) != 100 || len(batches[2]) != 5 {
		t.Fatalf("count batches = %v", []int{len(batches[0]), len(batches[1]), len(batches[2])})
	}
	big := make([]model.Observation, 0, 20)
	for i := 0; i < 20; i++ {
		path := strings.Repeat("p", 900)
		disks := make([]model.DiskReport, 40)
		for j := range disks {
			p := 1.0
			disks[j] = model.DiskReport{ID: fmt.Sprintf("d%d", j), Path: path, Percent: &p}
		}
		big = append(big, model.Observation{ID: fmt.Sprintf("home.boot.%d", i+1), External: &model.ExternalObservation{Report: &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "obj"}, Resource: &model.ResourceReport{Disks: disks}}}})
	}
	batches, err = splitPrepared(big, 512<<10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) < 2 {
		t.Fatalf("byte split did not split: %d", len(batches))
	}
	for _, batch := range batches {
		raw, _ := json.Marshal(batch)
		if len(raw) > 512<<10+1 {
			t.Fatalf("batch bytes = %d", len(raw))
		}
	}
}

func TestSourceBackoffIsBounded(t *testing.T) {
	for failures := 1; failures <= 12; failures++ {
		got := sourceBackoff(time.Second, failures)
		if got < 900*time.Millisecond || got > maxSourceBackoff+maxSourceBackoff/10 {
			t.Fatalf("backoff(%d) = %v", failures, got)
		}
		if failures >= 12 && got < maxSourceBackoff*9/10 {
			t.Fatalf("backoff(%d) did not reach its cap: %v", failures, got)
		}
	}
}

type isolationRuntime struct {
	goodReadings []source.Reading
	started      chan string
}

func (r *isolationRuntime) Validate(model.Config) error { return nil }
func (r *isolationRuntime) Collect(ctx context.Context, c model.Config, s model.Source, requests []source.Request) ([]source.Reading, error) {
	select {
	case r.started <- s.ID:
	default:
	}
	if s.ID == "bad" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.goodReadings, nil
}

func TestBadSourceDoesNotStarveAnotherSourceOrNativeCheck(t *testing.T) {
	c := sourceRunnerConfig(t, 1, model.ReportTypeStatus)
	c.Sources = append(c.Sources, model.Source{ID: "bad", Adapter: "synthetic-list", Observer: "home", Target: "https://bad.invalid/v1/objects", IntervalSeconds: 1, TimeoutSeconds: 2, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 10, MaxReportBytes: 16 << 10})
	c.Checks = append(c.Checks, model.Check{ID: "bad-status", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "bad", Object: "svc-bad", Report: model.ReportTypeStatus, Node: "aliyun", MaxAgeSeconds: 60}})
	c.Checks = append(c.Checks, model.Check{ID: "native-host", Observer: "home", Kind: "host", Host: &model.HostOptions{}})
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rt := &isolationRuntime{goodReadings: statusReadings(c, now.Add(-time.Minute)), started: make(chan string, 4)}
	seen := make(chan string, 8)
	s, err := New(c, "home", 1, func(ctx context.Context, c model.Config, x model.Check) model.Observation {
		return model.Observation{Check: x.ID, Observer: x.Observer, SampledAt: now, Outcome: "ok", Evidence: "measured", DurationMS: 1}
	}, func(v model.Observation) error {
		seen <- v.Check
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.WithSources(rt, func(obs []model.Observation) error {
		for _, v := range obs {
			seen <- v.Check
		}
		return nil
	}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	gotNative, gotGood, gotBadStart := false, false, false
	deadline := time.After(6 * time.Second)
	for !(gotNative && gotGood && gotBadStart) {
		select {
		case id := <-rt.started:
			if id == "bad" {
				gotBadStart = true
			}
		case check := <-seen:
			if check == "native-host" {
				gotNative = true
			}
			if check == "external-000" {
				gotGood = true
			}
		case <-deadline:
			t.Fatalf("isolated sources did not progress: native=%v good=%v bad=%v", gotNative, gotGood, gotBadStart)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop")
	}
}

func resourceReading(object string, at time.Time) source.Reading {
	percent := 25.0
	return source.Reading{Object: object, Report: model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: object, SampleID: "resource-" + object, ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}}}}
}

func dualReportConfig(t testing.TB) model.Config {
	t.Helper()
	c := testkit.Config()
	c.Checks = nil
	c.Sources = []model.Source{{ID: "probe-api", Adapter: testkit.SyntheticCombinedAdapter, Observer: "home", Target: "https://probe.invalid/combined", IntervalSeconds: 1, TimeoutSeconds: 2, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 1, MaxObjects: 10, MaxReportBytes: 64 << 10}}
	c.Checks = append(c.Checks,
		model.Check{ID: "combined-resource", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "svc", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 60, PrimaryDisk: "root"}},
		model.Check{ID: "combined-status", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "svc", Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 60}},
	)
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSourceSameObjectResourceAndStatusShareOneRead(t *testing.T) {
	c := dualReportConfig(t)
	now := time.Now().UTC()
	status := statusReadings(c, now.Add(-time.Minute))[0]
	rt := &fakeSourceRuntime{readings: []source.Reading{resourceReading("svc", now.Add(-time.Minute)), status}}
	var got []model.Observation
	s, err := New(c, "home", 1, func(context.Context, model.Config, model.Check) model.Observation {
		t.Fatal("native probe ran for external-only config")
		return model.Observation{}
	}, func(model.Observation) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s.WithSources(rt, func(obs []model.Observation) error { got = append(got, obs...); return nil }, 4)
	if err := s.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.calls.Load() != 1 || len(got) != 2 {
		t.Fatalf("calls=%d observations=%d", rt.calls.Load(), len(got))
	}
	seen := map[string]bool{}
	for _, o := range got {
		if o.Outcome != "ok" || o.External == nil || o.External.Report == nil {
			t.Fatalf("dual report attempt = %+v", o)
		}
		seen[o.Check] = true
	}
	if !seen["combined-resource"] || !seen["combined-status"] {
		t.Fatalf("checks = %+v", seen)
	}
	rt.mu.Lock()
	requests := rt.requests["probe-api"]
	rt.mu.Unlock()
	if len(requests) != 1 || requests[0].Object != "svc" || !requests[0].Wants(model.ReportTypeResource) || !requests[0].Wants(model.ReportTypeStatus) {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestSourceMissingReportTypeIsAnAttemptNotAnEntityOrOutage(t *testing.T) {
	c := dualReportConfig(t)
	now := time.Now().UTC()
	status := statusReadings(c, now.Add(-time.Minute))[0]
	rt := &fakeSourceRuntime{readings: []source.Reading{status}}
	var got []model.Observation
	s, _ := New(c, "home", 1, func(context.Context, model.Config, model.Check) model.Observation { return model.Observation{} }, func(model.Observation) error { return nil })
	s.WithSources(rt, func(obs []model.Observation) error { got = append(got, obs...); return nil }, 4)
	if err := s.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("observations = %d", len(got))
	}
	codes := map[string]string{}
	for _, o := range got {
		codes[o.Check] = o.Code
		if o.Connectivity {
			t.Fatal("attempt claimed measured connectivity")
		}
	}
	if codes["combined-status"] != "" || codes["combined-resource"] != "external_report_type_missing" {
		t.Fatalf("codes = %+v", codes)
	}
}

// A failed low-frequency source must not consume its upstream quota faster
// than a healthy source. Jitter remains symmetric around the nominal delay.
func TestSourceBackoffRespectsLongIntervals(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, 30 * time.Minute, time.Hour, 2 * time.Hour, 24 * time.Hour} {
		for _, failures := range []int{0, 1, 2, 12, 100} {
			nominal := interval
			if failures > 0 {
				nominal = min(interval*time.Duration(1<<min(failures, 12)), max(interval, maxSourceBackoff))
			}
			for i := 0; i < 20; i++ {
				got := sourceBackoff(interval, failures)
				if got < nominal*9/10 || got > nominal*11/10 {
					t.Fatalf("interval=%s failures=%d delay=%s nominal=%s", interval, failures, got, nominal)
				}
			}
		}
	}
}
