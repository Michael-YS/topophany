// Package testkit provides fixtures for tests. The synthetic source adapters
// in this file are never part of ProductionSourceAdapters and never execute
// from a real topology.
package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
)

const (
	SyntheticListAdapter     = "synthetic-list"
	SyntheticCounterAdapter  = "synthetic-counter"
	SyntheticStatusAdapter   = "synthetic-status"
	SyntheticCombinedAdapter = "synthetic-combined"
)

// SyntheticSourceAdapters is the test-only static registry. Tests pass it to
// probe.NewSourceRuntime explicitly; production registers only real adapters.
func SyntheticSourceAdapters() map[string]source.Adapter {
	return map[string]source.Adapter{
		SyntheticListAdapter:     ListAdapter{},
		SyntheticCounterAdapter:  &CounterAdapter{},
		SyntheticStatusAdapter:   StatusAdapter{},
		SyntheticCombinedAdapter: CombinedAdapter{},
	}
}

// SyntheticListPayload is enough of a list-response shape for an adapter to
// own pagination, latest-sample selection and field mapping.
type SyntheticListPayload struct {
	Epoch    string             `json:"epoch"`
	Machines []SyntheticMachine `json:"machines"`
	Next     string             `json:"next,omitempty"`
}

type SyntheticMachine struct {
	ID      string                   `json:"id"`
	Samples []SyntheticMachineSample `json:"samples"`
}

type SyntheticMachineSample struct {
	SampleID       string          `json:"sample_id"`
	ObservedAt     time.Time       `json:"observed_at"`
	CPUPercent     *float64        `json:"cpu_percent,omitempty"`
	CPUs           *int            `json:"cpus,omitempty"`
	CPUWindowMS    *int64          `json:"cpu_window_ms,omitempty"`
	CPUBasis       string          `json:"cpu_basis,omitempty"`
	MemoryTotal    *uint64         `json:"memory_total_bytes,omitempty"`
	MemoryUsed     *uint64         `json:"memory_used_bytes,omitempty"`
	MemoryWindowMS *int64          `json:"memory_available_bytes,omitempty"`
	MemoryBasis    string          `json:"memory_basis,omitempty"`
	Disks          []SyntheticDisk `json:"disks,omitempty"`
}

type SyntheticDisk struct {
	ID         string   `json:"id"`
	Path       string   `json:"path,omitempty"`
	TotalBytes *uint64  `json:"total_bytes,omitempty"`
	UsedBytes  *uint64  `json:"used_bytes,omitempty"`
	FreeBytes  *uint64  `json:"free_bytes,omitempty"`
	Percent    *float64 `json:"percent,omitempty"`
	Basis      string   `json:"basis,omitempty"`
}

type ListAdapter struct{}

func (ListAdapter) Validate(s model.Source) error {
	if s.Target == "" {
		return source.NewError("invalid_target", "list source needs a target")
	}
	return nil
}

func (ListAdapter) Collect(ctx context.Context, io source.IO, s model.Source, requests []source.Request) ([]source.Reading, error) {
	wanted := map[string]bool{}
	for _, request := range requests {
		if request.Wants(model.ReportTypeResource) {
			wanted[request.Object] = true
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	token, err := io.Credential(ctx, s)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Accept": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	next := ""
	epoch := ""
	machines := map[string]SyntheticMachine{}
	for page := 0; page < s.MaxPages; page++ {
		resp, err := io.Get(ctx, s, next, headers, s.MaxResponseBytes)
		if err != nil {
			return nil, err
		}
		if resp.Status != 200 {
			return nil, source.NewError("upstream_status", "list endpoint did not return 200")
		}
		var payload SyntheticListPayload
		if err := json.Unmarshal(resp.Body, &payload); err != nil {
			return nil, source.NewError("parse_error", "list response is not valid JSON")
		}
		if payload.Epoch != "" {
			epoch = payload.Epoch
		}
		for _, m := range payload.Machines {
			if m.ID == "" || len(m.Samples) == 0 {
				continue
			}
			cur, ok := machines[m.ID]
			if !ok {
				cur.ID = m.ID
			}
			cur.Samples = append(cur.Samples, m.Samples...)
			machines[m.ID] = cur
		}
		if payload.Next == "" {
			break
		}
		next = payload.Next
	}
	out := make([]source.Reading, 0, len(machines))
	ids := make([]string, 0, len(machines))
	for id := range machines {
		if wanted[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := machines[id]
		sort.SliceStable(m.Samples, func(i, j int) bool {
			if m.Samples[i].ObservedAt.Equal(m.Samples[j].ObservedAt) {
				return m.Samples[i].SampleID > m.Samples[j].SampleID
			}
			return m.Samples[i].ObservedAt.After(m.Samples[j].ObservedAt)
		})
		latest := m.Samples[0]
		rep := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: id, SampleID: latest.SampleID, Epoch: epoch, ObservedAt: latest.ObservedAt, TimeBasis: model.TimeBasisUpstreamSample, Fields: map[string]string{}}}
		cpu := model.CPUReport{}
		if latest.CPUPercent != nil {
			cpu.Percent = latest.CPUPercent
			cpu.Basis = latest.CPUBasis
		} else {
			rep.Fields["cpu.percent"] = model.FieldMissing
		}
		cpu.LogicalCores = latest.CPUs
		cpu.SampleWindowMS = latest.CPUWindowMS
		var memory *model.MemoryReport
		if latest.MemoryTotal != nil || latest.MemoryUsed != nil {
			memory = &model.MemoryReport{TotalBytes: latest.MemoryTotal, UsedBytes: latest.MemoryUsed, Basis: latest.MemoryBasis}
			if latest.MemoryTotal == nil {
				rep.Fields["memory.total_bytes"] = model.FieldUnsupported
			}
			if latest.MemoryUsed == nil {
				rep.Fields["memory.used_bytes"] = model.FieldMissing
			}
		}
		disks := make([]model.DiskReport, 0, len(latest.Disks))
		for _, d := range latest.Disks {
			disks = append(disks, model.DiskReport{ID: d.ID, Path: d.Path, TotalBytes: d.TotalBytes, UsedBytes: d.UsedBytes, FreeBytes: d.FreeBytes, Percent: d.Percent, Basis: d.Basis})
		}
		if len(rep.Fields) == 0 {
			rep.Fields = nil
		}
		rep.Resource = &model.ResourceReport{CPU: &cpu, Memory: memory, Disks: disks}
		out = append(out, source.Reading{Object: id, Report: rep})
	}
	return out, nil
}

// SyntheticCounterPayload is one page of cumulative counters. The adapter
// turns busy/total into a percent only from two samples over time.
type SyntheticCounterPayload struct {
	Epoch       string    `json:"epoch"`
	Sequence    uint64    `json:"sequence"`
	ObservedAt  time.Time `json:"observed_at"`
	Busy        uint64    `json:"busy"`
	Total       uint64    `json:"total"`
	CPUs        int       `json:"cpus"`
	MemoryUsed  uint64    `json:"memory_used_bytes,omitempty"`
	MemoryTotal uint64    `json:"memory_total_bytes,omitempty"`
}

type CounterAdapter struct {
	mu     sync.Mutex
	series *report.CounterSeries
}

func (a *CounterAdapter) Validate(s model.Source) error {
	if s.Target == "" {
		return source.NewError("invalid_target", "counter source needs a target")
	}
	return nil
}

func (a *CounterAdapter) Collect(ctx context.Context, io source.IO, s model.Source, requests []source.Request) ([]source.Reading, error) {
	object := "counter-" + s.ID
	wanted := false
	for _, request := range requests {
		wanted = wanted || request.Object == object && request.Wants(model.ReportTypeResource)
	}
	if !wanted {
		return nil, nil
	}
	token, err := io.Credential(ctx, s)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Accept": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	resp, err := io.Get(ctx, s, "", headers, s.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	if resp.Status != 200 {
		return nil, source.NewError("upstream_status", "counter endpoint did not return 200")
	}
	var p SyntheticCounterPayload
	if err := json.Unmarshal(resp.Body, &p); err != nil {
		return nil, source.NewError("parse_error", "counter response is not valid JSON")
	}
	if p.ObservedAt.IsZero() || p.Total == 0 {
		return nil, source.NewError("invalid_counter", "counter response lacks a sample time or total")
	}
	a.mu.Lock()
	if a.series == nil {
		a.series = report.NewCounterSeries(32)
	}
	busy := a.series.Observe(s.ID+"/"+p.Epoch+"/busy", p.Busy, p.ObservedAt, 5*time.Minute)
	total := a.series.Observe(s.ID+"/"+p.Epoch+"/total", p.Total, p.ObservedAt, 5*time.Minute)
	a.mu.Unlock()
	cpu := &model.CPUReport{LogicalCores: &p.CPUs, Basis: "counter_ratio"}
	if percent, ok := report.CounterPercent(busy, total); ok {
		cpu.Percent = &percent
		window := int64(busy.Seconds * 1000)
		if window < 1 {
			window = 1
		}
		cpu.SampleWindowMS = &window
	}
	rep := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: "counter-" + s.ID, SampleID: fmt.Sprintf("%s-%d", p.Epoch, p.Sequence), Epoch: p.Epoch, Sequence: &p.Sequence, ObservedAt: p.ObservedAt, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: cpu}}
	if p.MemoryTotal > 0 {
		used := p.MemoryUsed
		rep.Resource.Memory = &model.MemoryReport{TotalBytes: &p.MemoryTotal, UsedBytes: &used, Basis: "gauge"}
	}
	return []source.Reading{{Object: object, Report: rep}}, nil
}

// SyntheticStatusPayload is one status point, with an upstream latency that
// must never be confused with collection or API time.
type SyntheticStatusPayload struct {
	Object           string    `json:"object"`
	SampleID         string    `json:"sample_id"`
	ObservedAt       time.Time `json:"observed_at"`
	TimeBasis        string    `json:"time_basis,omitempty"`
	Status           string    `json:"status"`
	LatencyMS        *int64    `json:"latency_ms,omitempty"`
	ObservationPoint string    `json:"observation_point,omitempty"`
}

type StatusAdapter struct{}

func (StatusAdapter) Validate(s model.Source) error {
	if s.Target == "" {
		return source.NewError("invalid_target", "status source needs a target")
	}
	return nil
}

func (StatusAdapter) Collect(ctx context.Context, io source.IO, s model.Source, requests []source.Request) ([]source.Reading, error) {
	token, err := io.Credential(ctx, s)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Accept": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	resp, err := io.Get(ctx, s, "", headers, s.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	if resp.Status != 200 {
		return nil, source.NewError("upstream_status", "status endpoint did not return 200")
	}
	var p SyntheticStatusPayload
	if err := json.Unmarshal(resp.Body, &p); err != nil {
		return nil, source.NewError("parse_error", "status response is not valid JSON")
	}
	if p.Object == "" || p.Status == "" || p.ObservedAt.IsZero() && p.TimeBasis != model.TimeBasisUnknown {
		return nil, source.NewError("invalid_status", "status response lacks identity, state or sample time")
	}
	basis := p.TimeBasis
	if basis == "" {
		basis = model.TimeBasisUpstreamSample
	}
	rep := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: p.Object, SampleID: p.SampleID, ObservedAt: p.ObservedAt, TimeBasis: basis}, Status: &model.StatusReport{Status: p.Status, UpstreamLatencyMS: p.LatencyMS, ObservationPoint: p.ObservationPoint}}
	if p.SampleID == "" {
		return nil, source.NewError("invalid_status", "status response lacks a stable sample ID")
	}
	for _, request := range requests {
		if request.Object == p.Object && request.Wants(model.ReportTypeStatus) {
			return []source.Reading{{Object: p.Object, Report: rep}}, nil
		}
	}
	return nil, nil
}

// SyntheticCombinedPayload has a resource reading and a status reading for
// the same object, so one source read can feed two bindings without being
// mistaken for a duplicate object.
type SyntheticCombinedPayload struct {
	Object     string    `json:"object"`
	SampleID   string    `json:"sample_id"`
	ObservedAt time.Time `json:"observed_at"`
	CPUPercent *float64  `json:"cpu_percent,omitempty"`
	Status     string    `json:"status"`
	LatencyMS  *int64    `json:"latency_ms,omitempty"`
}

type CombinedAdapter struct{}

func (CombinedAdapter) Validate(s model.Source) error {
	if s.Target == "" {
		return source.NewError("invalid_target", "combined source needs a target")
	}
	return nil
}

func (CombinedAdapter) Collect(ctx context.Context, io source.IO, s model.Source, requests []source.Request) ([]source.Reading, error) {
	token, err := io.Credential(ctx, s)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Accept": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	resp, err := io.Get(ctx, s, "", headers, s.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	if resp.Status != 200 {
		return nil, source.NewError("upstream_status", "combined endpoint did not return 200")
	}
	var p SyntheticCombinedPayload
	if err := json.Unmarshal(resp.Body, &p); err != nil {
		return nil, source.NewError("parse_error", "combined response is not valid JSON")
	}
	if p.Object == "" || p.SampleID == "" || p.ObservedAt.IsZero() || p.Status == "" {
		return nil, source.NewError("invalid_combined", "combined response lacks identity, time or status")
	}
	if p.CPUPercent == nil {
		return nil, source.NewError("invalid_combined", "combined response lacks a resource reading")
	}
	for _, request := range requests {
		if request.Object != p.Object {
			continue
		}
		if request.Wants(model.ReportTypeResource) {
			percent := *p.CPUPercent
			rep := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: p.Object, SampleID: p.SampleID, ObservedAt: p.ObservedAt, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}}}
			if request.Wants(model.ReportTypeStatus) {
				status := p.Status
				latency := p.LatencyMS
				return []source.Reading{
					{Object: p.Object, Report: rep},
					{Object: p.Object, Report: model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: p.Object, SampleID: p.SampleID, ObservedAt: p.ObservedAt, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: status, UpstreamLatencyMS: latency}}},
				}, nil
			}
			return []source.Reading{{Object: p.Object, Report: rep}}, nil
		}
		if request.Wants(model.ReportTypeStatus) {
			status := p.Status
			latency := p.LatencyMS
			return []source.Reading{{Object: p.Object, Report: model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: p.Object, SampleID: p.SampleID, ObservedAt: p.ObservedAt, TimeBasis: model.TimeBasisUpstreamSample}, Status: &model.StatusReport{Status: status, UpstreamLatencyMS: latency}}}}, nil
		}
	}
	return nil, nil
}
