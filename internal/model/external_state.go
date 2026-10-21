package model

import "time"

// ExternalState is the collector-owned acceptance state for external sources
// and bindings. It is persisted with the checkpoint, not inferred from raw
// sample tails. Schema is the external-state schema, independent of the
// checkpoint and wire schemas.
type ExternalState struct {
	Schema   int                     `json:"schema"`
	Sources  map[string]SourceState  `json:"sources,omitempty"`
	Bindings map[string]BindingState `json:"bindings,omitempty"`
}

const ExternalStateSchema = 1

func NewExternalState() *ExternalState {
	return &ExternalState{Schema: ExternalStateSchema, Sources: map[string]SourceState{}, Bindings: map[string]BindingState{}}
}

// SourceState is one source's contact and attempt diagnostics. LastContact
// advances only on a successful read; failures update the attempt fields but
// never extend contact freshness.
type SourceState struct {
	ID             string    `json:"id"`
	LastContact    time.Time `json:"last_contact,omitzero"`
	LastAttemptAt  time.Time `json:"last_attempt_at,omitzero"`
	LastResult     string    `json:"last_result,omitempty"` // ok, error
	LastCode       string    `json:"last_code,omitempty"`
	LastDurationMS int64     `json:"last_duration_ms,omitempty"`
}

// ReportCursor is the accepted report ordering identity. It is written with
// the report so a replay can be classified after raw samples are trimmed.
type ReportCursor struct {
	Mode       string    `json:"mode"` // sample, sequence, time
	Identity   string    `json:"identity"`
	SampleID   string    `json:"sample_id,omitempty"`
	Epoch      string    `json:"epoch,omitempty"`
	Sequence   *uint64   `json:"sequence,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	TimeBasis  string    `json:"time_basis,omitempty"`
	Hash       string    `json:"hash"`
}

// SeenSample is a bounded recent sample-ID record. It detects duplicate IDs
// after the current cursor has moved and raw samples have been trimmed.
type SeenSample struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

// BindingState is the persisted acceptance and confirmation state of one
// external check under one contract.
type BindingState struct {
	Check         string `json:"check"`
	Contract      string `json:"contract"`
	Source        string `json:"source"`
	Object        string `json:"object"`
	ReportType    string `json:"report_type"`
	MaxAgeSeconds int    `json:"max_age_seconds"`

	Cursor          ReportCursor    `json:"cursor"`
	Last            *ExternalReport `json:"last,omitempty"`
	DataDeadline    time.Time       `json:"data_deadline,omitzero"`
	FirstAcceptedAt time.Time       `json:"first_accepted_at,omitzero"`
	LastAcceptedAt  time.Time       `json:"last_accepted_at,omitzero"`
	Basis           string          `json:"basis,omitempty"`
	Seen            []SeenSample    `json:"seen,omitempty"`
	LastResult      string          `json:"last_result,omitempty"` // new, duplicate, late, conflict, unusable
	LastCode        string          `json:"last_code,omitempty"`

	FailureThreshold  int  `json:"failure_threshold,omitempty"`
	RecoveryThreshold int  `json:"recovery_threshold,omitempty"`
	PolicyThreshold   int  `json:"policy_threshold,omitempty"`
	ConfirmedDown     bool `json:"confirmed_down,omitempty"`
	PolicyConfirmed   bool `json:"policy_confirmed,omitempty"`

	UpRun     int64 `json:"up_run,omitempty"`
	DownRun   int64 `json:"down_run,omitempty"`
	PolicyRun int64 `json:"policy_run,omitempty"`

	ReportSamples   int64 `json:"report_samples,omitempty"`
	ResourceSamples int64 `json:"resource_samples,omitempty"`
	StatusSamples   int64 `json:"status_samples,omitempty"`
	StatusUp        int64 `json:"status_up,omitempty"`
	StatusDegraded  int64 `json:"status_degraded,omitempty"`
	StatusDown      int64 `json:"status_down,omitempty"`
	StatusUnknown   int64 `json:"status_unknown,omitempty"`

	PolicyViolations int64 `json:"policy_violations,omitempty"`
	PolicyUnknowns   int64 `json:"policy_unknowns,omitempty"`

	UpstreamLatencySamples int64 `json:"upstream_latency_samples,omitempty"`
	UpstreamLatencySumMS   int64 `json:"upstream_latency_sum_ms,omitempty"`
	UpstreamLatencyMaxMS   int64 `json:"upstream_latency_max_ms,omitempty"`

	Duplicates int64 `json:"duplicates,omitempty"`
	Late       int64 `json:"late,omitempty"`
	Conflicts  int64 `json:"conflicts,omitempty"`
	Unusable   int64 `json:"unusable,omitempty"`
}

// CloneExternalState detaches all mutable state maps and reports.
func CloneExternalState(in *ExternalState) *ExternalState {
	if in == nil {
		return nil
	}
	out := *in
	out.Sources = make(map[string]SourceState, len(in.Sources))
	for k, v := range in.Sources {
		out.Sources[k] = v
	}
	out.Bindings = make(map[string]BindingState, len(in.Bindings))
	for k, v := range in.Bindings {
		out.Bindings[k] = CloneBindingState(v)
	}
	return &out
}

func CloneBindingState(in BindingState) BindingState {
	out := in
	out.Cursor.Sequence = nil
	if in.Cursor.Sequence != nil {
		v := *in.Cursor.Sequence
		out.Cursor.Sequence = &v
	}
	if in.Last != nil {
		v := CloneExternalReport(*in.Last)
		out.Last = &v
	}
	out.Seen = append([]SeenSample(nil), in.Seen...)
	return out
}

// ExternalRule carries the non-measurement thresholds that govern an
// external binding's confirmation. They are not part of its measurement
// contract, so editing them preserves history but re-applies to stored runs.
type ExternalRule struct {
	FailureThreshold  int
	RecoveryThreshold int
	PolicyThreshold   int
}
