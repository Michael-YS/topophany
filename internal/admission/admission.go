// Package admission validates a live external observation once for every
// collector entry point. It deliberately takes the clock and its tolerance
// from the caller: a pure model function must not read system time, and
// journal replay must not re-judge an already durable record as live.
package admission

import (
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

// Error is a bounded refusal, carrying a stable code and no upstream text.
type Error struct {
	Code string
}

func (e *Error) Error() string { return e.Code }

// Code returns the refusal code of a report or admission error, or "".
func Code(err error) string {
	var a *Error
	if errors.As(err, &a) {
		return a.Code
	}
	return report.Code(err)
}

func refuse(code string) error { return &Error{Code: code} }

// Window is the caller's live ingestion boundary. Now and Future are the
// collector's wall clock and its monotonic high-water mark; a clock step-back
// must not reject a report that was valid under the earlier high-water mark.
// MaxAge is the configured ingestion window and Skew the explicit allowance
// for scheduling and transport jitter.
type Window struct {
	Now    time.Time
	Future time.Time
	MaxAge time.Duration
	Skew   time.Duration
}

func (w Window) valid() bool {
	return !w.Now.IsZero() && !w.Future.IsZero() && !w.Future.Before(w.Now) && w.MaxAge > 0 && w.Skew >= 0
}

// contains reports whether t is inside the live window. Zero is never inside:
// a missing attempt or envelope time is a shape error, not an old sample.
func (w Window) contains(t time.Time) bool {
	return !t.IsZero() && !t.After(w.Future.Add(w.Skew)) && !t.Before(w.Now.Add(-w.MaxAge))
}

// External validates one live external observation and returns its owned,
// normalized report. It combines the report shape/identity checks with the
// envelope and attempt time relations and the encoded observation bounds, so
// the API and a direct local collector cannot disagree about admission.
func External(v model.Observation, x model.Check, s model.Source, w Window) (*model.ExternalReport, error) {
	if !w.valid() {
		return nil, refuse("admission_window_invalid")
	}
	normalized, err := report.ValidateExternalObservation(v, x, s)
	if err != nil {
		return nil, err
	}
	if !w.contains(v.SampledAt) {
		return nil, refuse("sample_time_out_of_range")
	}
	if v.External == nil {
		// A native observation admitted through an external binding has
		// already been refused by ValidateExternalObservation.
		return nil, nil
	}
	if !w.contains(v.External.AttemptedAt) {
		return nil, refuse("external_attempt_time_out_of_range")
	}
	if normalized != nil {
		at, known := report.SampleAt(*normalized)
		if known {
			if at.Year() < 1970 || !w.containsReport(at) {
				return nil, refuse("external_report_time_out_of_range")
			}
			// A report cannot claim to have been observed after the collection
			// that returned it. Use the later of the attempt finish and the
			// envelope start so a collector clock step-back during one read does
			// not turn the same observation into a target outage.
			finished := v.External.AttemptedAt
			if v.SampledAt.After(finished) {
				finished = v.SampledAt
			}
			if at.After(finished.Add(w.Skew)) {
				return nil, refuse("external_report_time_out_of_range")
			}
			if normalized.TimeBasis == model.TimeBasisLiveRead && at.Before(v.SampledAt.Add(-w.Skew)) {
				return nil, refuse("external_report_time_out_of_range")
			}
		}
	}
	took := time.Duration(x.TimeoutSeconds+5) * time.Second
	if code := config.ObservationBounds(v, took); code != "" {
		return nil, refuse(code)
	}
	return normalized, nil
}

// containsReport bounds a report's sample time. Unlike the envelope and
// attempt, an old sample is allowed: it is stored as historical evidence and
// never becomes current. Only impossible future times are refused here.
func (w Window) containsReport(at time.Time) bool {
	return !at.After(w.Future.Add(w.Skew))
}
