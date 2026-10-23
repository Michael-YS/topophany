package direct

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/admission"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
	"time"
)

// Record atomically admits a whole successful source batch. Failed adapter
// reads never commit their successful prefix. It validates ownership and the
// execution generation before any state mutation and sets real ReceivedAt.
func (s *Session) Record(generation, id string, started, finished time.Time, readings []source.Reading, collectErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil || s.loops[id] == nil || generation != s.loops[id].generation {
		return fail("profile_conflict")
	}
	src, ok := config.Source(s.config, id)
	if !ok {
		return fail("source_not_found")
	}
	checks, _ := s.requests(src)
	received := s.opts.Now().UTC()
	if received.After(s.high) {
		s.high = received
	}
	if s.accounted != s.external {
		sizes, err := report.MeasureState(s.external)
		if err != nil {
			return fail("state_budget")
		}
		s.sizes, s.accounted = sizes, s.external
	}
	update := report.BeginState(s.external)
	next := update.State
	code := ""
	if collectErr != nil {
		code = AsError(collectErr).Code
		readings = nil
	}
	if len(readings) > 40 {
		return fail("state_budget")
	}
	reports := map[source.Key]model.ExternalReport{}
	batchBytes := 0
	for _, r := range readings {
		key := source.Key{Object: r.Object, Report: report.PayloadType(r.Report)}
		if _, exists := reports[key]; exists {
			return fail("upstream_incompatible")
		}
		reports[key] = r.Report
		b, e := json.Marshal(r.Report)
		if e != nil {
			return fail("state_budget")
		}
		batchBytes += len(b)
		if batchBytes > MaxBatchBytes {
			return fail("state_budget")
		}
	}
	for _, x := range checks {
		o := model.Observation{Project: s.config.Project, Observer: x.Observer, Check: x.ID, Contract: config.Contract(s.config, x), SampledAt: started, ReceivedAt: received, Outcome: "error", Code: code, Evidence: "reported", Stage: "external_source", PathEvidence: "external_source", DurationMS: max(0, finished.Sub(started).Milliseconds()), External: &model.ExternalObservation{Source: id, SourceGeneration: generation, AttemptedAt: finished, Contact: collectErr == nil}}
		if r, ok := reports[source.Key{Object: x.External.Object, Report: x.External.Report}]; ok && collectErr == nil {
			o.External.Report = &r
			o.Outcome = "ok"
			o.Code = ""
		} else if o.Code == "" {
			o.Code = "external_object_missing"
		}
		if o.Project != s.config.Project || o.Observer != src.Observer || o.Contract != config.Contract(s.config, x) {
			return fail("upstream_incompatible")
		}
		normalized, e := admission.External(o, x, src, admission.Window{Now: received, Future: s.high, MaxAge: 24 * time.Hour, Skew: 30 * time.Second})
		if e != nil {
			return fail("upstream_incompatible")
		}
		o.External.Report = normalized
		update.Apply(o, *x.External, model.ExternalRule{FailureThreshold: x.FailureThreshold, RecoveryThreshold: x.RecoveryThreshold, PolicyThreshold: x.PolicyThreshold})
	}
	// Reject undeclared result pairs instead of retaining arbitrary upstream keys.
	for key := range reports {
		found := false
		for _, x := range checks {
			found = found || x.External.Object == key.Object && x.External.Report == key.Report
		}
		if !found {
			return fail("upstream_incompatible")
		}
	}
	sizes, e := s.sizes.Updated(update)
	limit := s.opts.MaxSourceStateBytes
	if len(s.config.Sources) <= 1 {
		limit = s.opts.MaxStateBytes
	}
	if e != nil || sizes.Total > int64(s.opts.MaxStateBytes) || sizes.Sources[id] > int64(limit) || len(next.Bindings) > model.MaxExternalChecks || len(next.Sources) > model.MaxSources {
		return fail("state_budget")
	}
	s.external, s.accounted, s.sizes = next, next, sizes
	s.prepared = s.prepared.WithExternalChanges(update.Sources, update.Bindings)
	return nil
}
