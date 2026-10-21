package report_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

func f(v float64) *float64 { return &v }
func u(v uint64) *uint64   { return &v }
func i(v int) *int         { return &v }
func n(v int64) *int64     { return &v }

func meta(object, basis string, at time.Time) model.ReportMeta {
	return model.ReportMeta{Schema: model.ReportSchema, Object: object, ObservedAt: at, TimeBasis: basis}
}

func TestNormalizePartialResourceKeepsNullsAndUnits(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	r := model.ExternalReport{
		ReportMeta: meta("vm/one", model.TimeBasisUpstreamSample, at),
		Resource: &model.ResourceReport{
			CPU:    &model.CPUReport{Percent: f(0), LogicalCores: i(4), SampleWindowMS: n(250), Basis: "host_cpu"},
			Memory: &model.MemoryReport{TotalBytes: u(1024), UsedBytes: u(256), Percent: f(25), Basis: "available"},
			Disks:  []model.DiskReport{{ID: "root", Path: "/", TotalBytes: u(2048), UsedBytes: u(512), Percent: f(25), Basis: "free"}},
		},
	}
	got, err := report.Normalize(r, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if got.Resource.CPU.Percent == nil || *got.Resource.CPU.Percent != 0 {
		t.Fatal("explicit zero normalized away")
	}
	if got.Resource.Memory.AvailableBytes != nil {
		t.Fatal("missing bytes became a value")
	}
	if !report.EqualContent(got, got) {
		t.Fatal("canonical report is not stable")
	}
	// Normalization owns its pointers and slices.
	*got.Resource.CPU.Percent = 99
	got.Resource.Disks[0].ID = "changed"
	if *r.Resource.CPU.Percent != 0 || r.Resource.Disks[0].ID != "root" {
		t.Fatal("normalization aliases input")
	}
}

func TestNormalizeRejectsUnsafeNumericAndCapacityValues(t *testing.T) {
	at := time.Now().UTC()
	cases := map[string]func(*model.ExternalReport){
		"no payload": func(r *model.ExternalReport) { r.Resource = nil },
		"two payloads": func(r *model.ExternalReport) {
			r.Status = &model.StatusReport{Status: "up"}
		},
		"NaN percent":      func(r *model.ExternalReport) { *r.Resource.CPU.Percent = math.NaN() },
		"Inf percent":      func(r *model.ExternalReport) { *r.Resource.CPU.Percent = math.Inf(1) },
		"negative percent": func(r *model.ExternalReport) { *r.Resource.CPU.Percent = -1 },
		"high percent":     func(r *model.ExternalReport) { *r.Resource.CPU.Percent = 101 },
		"zero cores":       func(r *model.ExternalReport) { *r.Resource.CPU.LogicalCores = 0 },
		"sample window":    func(r *model.ExternalReport) { *r.Resource.CPU.SampleWindowMS = 0 },
		"used over total": func(r *model.ExternalReport) {
			r.Resource.Memory = &model.MemoryReport{TotalBytes: u(10), UsedBytes: u(11)}
		},
		"available over total": func(r *model.ExternalReport) {
			r.Resource.Memory = &model.MemoryReport{TotalBytes: u(10), AvailableBytes: u(11)}
		},
		"used plus available": func(r *model.ExternalReport) {
			r.Resource.Memory = &model.MemoryReport{TotalBytes: u(10), UsedBytes: u(8), AvailableBytes: u(3)}
		},
		"zero total": func(r *model.ExternalReport) {
			r.Resource.Memory = &model.MemoryReport{TotalBytes: u(0), UsedBytes: u(0)}
		},
		"disk used": func(r *model.ExternalReport) {
			r.Resource.Disks = []model.DiskReport{{ID: "root", TotalBytes: u(10), UsedBytes: u(11)}}
		},
		"disk free": func(r *model.ExternalReport) {
			r.Resource.Disks = []model.DiskReport{{ID: "root", TotalBytes: u(10), FreeBytes: u(11)}}
		},
		"disk sum": func(r *model.ExternalReport) {
			r.Resource.Disks = []model.DiskReport{{ID: "root", TotalBytes: u(10), UsedBytes: u(9), FreeBytes: u(2)}}
		},
		"duplicate disk": func(r *model.ExternalReport) {
			r.Resource.Disks = []model.DiskReport{{ID: "root", Percent: f(1)}, {ID: "root", Percent: f(2)}}
		},
		"unsafe disk ID": func(r *model.ExternalReport) {
			r.Resource.Disks = []model.DiskReport{{ID: "root\n", Percent: f(1)}}
		},
		"unknown status": func(r *model.ExternalReport) {
			r.Resource = nil
			r.Status = &model.StatusReport{Status: "maybe"}
		},
		"negative latency": func(r *model.ExternalReport) {
			r.Resource = nil
			r.Status = &model.StatusReport{Status: "up", UpstreamLatencyMS: n(-1)}
		},
		"unknown time basis":  func(r *model.ExternalReport) { r.TimeBasis = "read_clock" },
		"missing sample time": func(r *model.ExternalReport) { r.ObservedAt = time.Time{} },
		"unknown schema":      func(r *model.ExternalReport) { r.Schema = 2 },
		"unsafe object":       func(r *model.ExternalReport) { r.Object = "vm one" },
		"empty resource":      func(r *model.ExternalReport) { r.Resource = &model.ResourceReport{} },
		"too many fields": func(r *model.ExternalReport) {
			r.Fields = map[string]string{}
			for i := 0; i < model.MaxReportFields+1; i++ {
				r.Fields[fmt.Sprintf("field.%d", i)] = model.FieldMissing
			}
		},
		"bad field status": func(r *model.ExternalReport) { r.Fields = map[string]string{"cpu.percent": "maybe"} },
		"known but absent": func(r *model.ExternalReport) {
			r.Fields = map[string]string{"cpu.percent": model.FieldKnown}
			r.Resource.CPU.Percent = nil
		},
		"present but invalid": func(r *model.ExternalReport) {
			r.Fields = map[string]string{"cpu.percent": model.FieldInvalid}
		},
		"foreign field": func(r *model.ExternalReport) { r.Fields = map[string]string{"gpu.percent": model.FieldMissing} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := model.ExternalReport{
				ReportMeta: meta("vm/one", model.TimeBasisUpstreamSample, at),
				Resource:   &model.ResourceReport{CPU: &model.CPUReport{Percent: f(10), LogicalCores: i(2), SampleWindowMS: n(100)}},
			}
			change(&r)
			if _, err := report.Normalize(r, 4096); err == nil {
				t.Fatal("invalid report accepted")
			} else if report.Code(err) == "" {
				t.Fatalf("refusal has no code: %v", err)
			}
		})
	}
}

func TestNormalizeFieldAvailabilityAndReportBudget(t *testing.T) {
	r := model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "vm/one", ObservedAt: time.Now().UTC(), TimeBasis: model.TimeBasisLiveRead,
			Fields: map[string]string{"cpu.percent": model.FieldKnown, "memory.total_bytes": model.FieldUnsupported, "disk.root.percent": model.FieldInvalid}},
		Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: f(1)}, Disks: []model.DiskReport{{ID: "root", Path: "/"}}},
	}
	if _, err := report.Normalize(r, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := report.Normalize(r, 128); err == nil || report.Code(err) != "report_too_large" {
		t.Fatalf("encoded report budget not enforced: %v", err)
	}
}

func TestDeadlinesDoNotInferTime(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	r := model.ExternalReport{ReportMeta: meta("vm/one", model.TimeBasisUpstreamSample, at)}
	if got, ok := report.SampleAt(r); !ok || !got.Equal(at) {
		t.Fatal("upstream sample time lost")
	}
	deadline, ok := report.DataDeadline(r, 2*time.Minute)
	if !ok || !deadline.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("data deadline = %v, %v", deadline, ok)
	}
	if _, ok := report.DataDeadline(r, 0); ok {
		t.Fatal("zero max age produced a deadline")
	}
	r.TimeBasis = model.TimeBasisUnknown
	if _, ok := report.SampleAt(r); ok {
		t.Fatal("unknown time basis became evidence")
	}
	if _, ok := report.DataDeadline(r, time.Minute); ok {
		t.Fatal("unknown time basis produced a data deadline")
	}
	contact, ok := report.ContactDeadline(at.Add(time.Second), 30*time.Second)
	if !ok || !contact.Equal(at.Add(31*time.Second)) {
		t.Fatalf("contact deadline = %v, %v", contact, ok)
	}
	if _, ok := report.ContactDeadline(time.Time{}, time.Minute); ok {
		t.Fatal("zero contact produced a deadline")
	}
	data := at.Add(time.Minute)
	snapshot := at.Add(10 * time.Second)
	if got := report.ValidUntil(data, contact, snapshot); !got.Equal(snapshot) {
		t.Fatalf("valid until = %v", got)
	}
}

func TestResourceAssertionsDistinguishViolationMissingAndPass(t *testing.T) {
	binding := model.ExternalBinding{Report: model.ReportTypeResource, PrimaryDisk: "root", Assertions: &model.ResourceAssertions{CPUPercentMax: f(80), MemoryPercentMax: f(90), DiskPercentMax: f(85)}}
	full := model.ExternalReport{ReportMeta: meta("vm/one", model.TimeBasisUnknown, time.Time{}), Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: f(50)}, Memory: &model.MemoryReport{Percent: f(40)}, Disks: []model.DiskReport{{ID: "root", Percent: f(20)}}}}
	if got := report.Evaluate(binding, full); got.Outcome != report.PolicyPass {
		t.Fatalf("pass = %+v", got)
	}
	violate := full
	violate.Resource = &model.ResourceReport{CPU: full.Resource.CPU, Memory: full.Resource.Memory, Disks: []model.DiskReport{{ID: "root", Percent: f(99)}}}
	if got := report.Evaluate(binding, violate); got.Outcome != report.PolicyViolation || len(got.Violations) != 1 || got.Violations[0] != "disk" {
		t.Fatalf("violation = %+v", got)
	}
	// A known violation wins over another required field that is missing.
	partial := model.ExternalReport{ReportMeta: full.ReportMeta, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: f(90)}, Memory: &model.MemoryReport{}}}
	if got := report.Evaluate(binding, partial); got.Outcome != report.PolicyViolation || len(got.Missing) == 0 {
		t.Fatalf("mixed violation/missing = %+v", got)
	}
	missing := model.ExternalReport{ReportMeta: full.ReportMeta, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: f(10)}}}
	if got := report.Evaluate(binding, missing); got.Outcome != report.PolicyUnknown {
		t.Fatalf("missing required fields = %+v", got)
	}
	noDisk := model.ExternalReport{ReportMeta: full.ReportMeta, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: f(10)}, Memory: &model.MemoryReport{Percent: f(10)}}}
	if got := report.Evaluate(binding, noDisk); got.Outcome != report.PolicyUnknown {
		t.Fatalf("missing primary disk = %+v", got)
	}
	if got := report.Evaluate(model.ExternalBinding{Report: model.ReportTypeResource}, full); got.Outcome != report.PolicyPass {
		t.Fatalf("no assertions = %+v", got)
	}
}

func TestCounterSeriesFirstResetWindowIsolationAndLimit(t *testing.T) {
	at := time.Unix(1000, 0)
	c := report.NewCounterSeries(2)
	if r := c.Observe("a", 10, at, time.Minute); !r.Seeded || r.Valid {
		t.Fatalf("first sample = %+v", r)
	}
	r := c.Observe("a", 16, at.Add(2*time.Second), time.Minute)
	if !r.Valid || r.Delta != 6 || r.RatePerSecond != 3 {
		t.Fatalf("second sample = %+v", r)
	}
	if p, ok := report.CounterPercent(report.CounterRate{Valid: true, Seconds: 2, RatePerSecond: 3}, report.CounterRate{Valid: true, Seconds: 2, RatePerSecond: 6}); !ok || p != 50 {
		t.Fatalf("percent = %v, %v", p, ok)
	}
	if r := c.Observe("a", 1, at.Add(3*time.Second), time.Minute); !r.Reset || r.Valid {
		t.Fatalf("counter reset = %+v", r)
	}
	if r := c.Observe("a", 2, at.Add(time.Second), time.Minute); r.Anomaly != report.CounterTimeBackwards {
		t.Fatalf("backwards time = %+v", r)
	}
	if r := c.Observe("a", 2, at.Add(3*time.Second), time.Minute); r.Anomaly != report.CounterZeroInterval {
		t.Fatalf("zero interval = %+v", r)
	}
	if r := c.Observe("a", 8, at.Add(2*time.Minute), time.Minute); r.Anomaly != report.CounterWindowExceeded || r.Valid {
		t.Fatalf("window = %+v", r)
	}
	if r := c.Observe("b", 1, at, time.Minute); !r.Seeded {
		t.Fatalf("second series = %+v", r)
	}
	if r := c.Observe("c", 1, at, time.Minute); r.Anomaly != report.CounterSeriesLimit {
		t.Fatalf("series limit = %+v", r)
	}
	if p, ok := report.CounterPercent(report.CounterRate{Valid: true, Seconds: 2, RatePerSecond: 4}, report.CounterRate{Valid: true, Seconds: 2, RatePerSecond: 3}); ok {
		t.Fatalf("part above total produced %v", p)
	}
	if _, ok := report.CounterPercent(report.CounterRate{Valid: true, Seconds: 2, RatePerSecond: 1}, report.CounterRate{Valid: true, Seconds: 3, RatePerSecond: 2}); ok {
		t.Fatal("different intervals were combined")
	}
}
