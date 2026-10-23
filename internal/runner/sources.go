package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// SourceRuntime is the bounded executor supplied by probe. Validate runs
// before any source is scheduled; Collect performs exactly one source read.
type SourceRuntime interface {
	Validate(model.Config) error
	Collect(context.Context, model.Config, model.Source, []source.Request) ([]source.Reading, error)
}

// BatchSink persists a fan-out batch. A nil BatchSink falls back to the
// observation Sink for compatibility with native-only callers.
type BatchSink func([]model.Observation) error

const (
	maxSourceWorkers = 4
	sourceBatchBytes = 512 << 10
	sourceBatchCount = 100
	maxSourceBackoff = time.Hour
)

// WithSources wires the static source runtime. It returns the scheduler so a
// caller can keep the existing New call shape. Invalid settings are refused
// by Run/Once before execution, not silently coerced.
func (s *Scheduler) WithSources(rt SourceRuntime, sink BatchSink, workers int) *Scheduler {
	s.sources = rt
	s.sourceSink = sink
	s.sourceWorkers = workers
	return s
}

type sourceJob struct{ index int }
type sourceResult struct {
	index        int
	observations []model.Observation
	failed       bool
}

// localSources returns sources this observer can execute and that have at
// least one enabled external binding, in topology order.
func (s *Scheduler) localSources() ([]model.Source, map[string][]model.Check) {
	bySource := map[string][]model.Check{}
	for _, x := range s.Config.Checks {
		if x.Kind != "external" || x.Observer != s.Observer || x.Disabled || x.Maintenance || x.External == nil {
			continue
		}
		bySource[x.External.Source] = append(bySource[x.External.Source], x)
	}
	out := []model.Source{}
	for _, src := range s.Config.Sources {
		if src.Observer == s.Observer && len(bySource[src.ID]) > 0 {
			out = append(out, src)
		}
	}
	return out, bySource
}

func (s *Scheduler) sourceGeneration(src model.Source) string {
	// Never hash the target or credential reference: their query strings and
	// names can themselves be low-entropy secrets. The runtime builds a fresh
	// IO and client for every read, so route/TLS or credential-value changes
	// do not leave a reusable session. This value identifies the scheduler
	// boot and source instance for rejecting an old execution generation.
	h := sha256.New()
	h.Write([]byte(s.Boot))
	h.Write([]byte{0})
	h.Write([]byte(src.ID))
	h.Write([]byte{0})
	h.Write([]byte(src.Adapter))
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// collectSource turns one runtime read into report observations for exactly
// the declared enabled bindings. A source error becomes an attempt diagnostic
// on each binding, never a target failure.
func (s *Scheduler) collectSource(ctx context.Context, src model.Source, checks []model.Check) ([]model.Observation, bool) {
	started := time.Now().UTC()
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(src.TimeoutSeconds)*time.Second)
	defer cancel()
	requests := source.RequestsForChecks(checks)
	readings, err := s.sources.Collect(jobCtx, s.Config, src, requests)
	finished := time.Now().UTC()
	if ctx.Err() != nil {
		return nil, false
	}
	elapsed := finished.Sub(started)
	if elapsed < 0 {
		elapsed = 0
	}
	generation := s.sourceGeneration(src)
	if err != nil {
		code := source.Code(err)
		out := make([]model.Observation, 0, len(checks))
		for _, x := range checks {
			out = append(out, s.sourceAttempt(x, src, generation, started, finished, elapsed, code, false, nil))
		}
		return out, true
	}
	byKey := make(map[source.Key]source.Reading, len(readings))
	gotObject := make(map[string]bool, len(readings))
	for _, reading := range readings {
		gotObject[reading.Object] = true
		byKey[source.Key{Object: reading.Object, Report: report.PayloadType(reading.Report)}] = reading
	}
	out := make([]model.Observation, 0, len(checks))
	for _, x := range checks {
		reading, ok := byKey[source.Key{Object: x.External.Object, Report: x.External.Report}]
		switch {
		case !ok:
			code := "external_report_type_missing"
			if !gotObject[x.External.Object] {
				code = "external_object_missing"
			}
			out = append(out, s.sourceAttempt(x, src, generation, started, finished, elapsed, code, true, nil))
		case !reportTypeMatches(x, reading.Report):
			out = append(out, s.sourceAttempt(x, src, generation, started, finished, elapsed, "external_report_type_mismatch", true, nil))
		default:
			r := model.CloneExternalReport(reading.Report)
			out = append(out, s.sourceAttempt(x, src, generation, started, finished, elapsed, "", true, &r))
		}
	}
	return out, false
}

func reportTypeMatches(x model.Check, r model.ExternalReport) bool {
	return report.PayloadType(r) == x.External.Report
}

func (s *Scheduler) sourceAttempt(x model.Check, src model.Source, generation string, started, finished time.Time, elapsed time.Duration, code string, contact bool, r *model.ExternalReport) model.Observation {
	o := model.Observation{Project: s.Config.Project, Contract: config.Contract(s.Config, x), Check: x.ID, Observer: x.Observer, SampledAt: started, Outcome: "ok", Evidence: "reported", Stage: "external_source", DurationMS: elapsed.Milliseconds(), Connectivity: false, PathEvidence: "external_source"}
	if r != nil {
		o.Detail = "source read produced the declared upstream report"
	} else {
		o.Outcome = "error"
		o.Code = code
		o.Detail = "source read did not produce a usable report: " + code
	}
	o.External = &model.ExternalObservation{Source: src.ID, SourceGeneration: generation, AttemptedAt: finished, Contact: contact, Report: r}
	return o
}

// sendFanout stamps observations and splits them by encoded bytes and count
// before one sink call per batch. It refuses an observation larger than the
// batch budget instead of silently exceeding it.
func (s *Scheduler) sendFanout(observations []model.Observation) error {
	for i := range observations {
		observations[i] = s.stamp(observations[i])
	}
	if s.sourceSink == nil {
		for _, v := range observations {
			if err := s.Sink(v); err != nil {
				return err
			}
		}
		return nil
	}
	batches, err := splitPrepared(observations, sourceBatchBytes)
	if err != nil {
		return err
	}
	for _, batch := range batches {
		if err := s.sourceSink(batch); err != nil {
			return err
		}
	}
	return nil
}

// splitPrepared bounds a fan-out by both encoded observation bytes and the
// existing 100-observation wire batch limit.
func splitPrepared(observations []model.Observation, maxBytes int) ([][]model.Observation, error) {
	if maxBytes < 1024 {
		return nil, errors.New("fan-out batch byte budget is too small")
	}
	out := [][]model.Observation{}
	current := []model.Observation{}
	size := 2
	for _, v := range observations {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		if len(raw) > maxBytes-2 {
			return nil, errors.New("one external observation exceeds the fan-out batch budget")
		}
		if len(current) >= sourceBatchCount || size+len(raw)+1 > maxBytes {
			out = append(out, current)
			current = []model.Observation{}
			size = 2
		}
		current = append(current, v)
		size += len(raw) + 1
	}
	if len(current) > 0 {
		out = append(out, current)
	}
	return out, nil
}

func sourceBackoff(interval time.Duration, failures int) time.Duration {
	if failures < 1 {
		return jitter(interval)
	}
	shift := min(failures, 12)
	delay := interval * time.Duration(1<<shift)
	if delay > maxSourceBackoff || delay < interval {
		delay = maxSourceBackoff
	}
	return jitter(delay)
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(float64(d) * (0.9 + rand.Float64()*0.2))
}
