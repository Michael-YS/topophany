package model

import "time"

// ExternalBindingView is the sanitized snapshot projection of one external
// binding's persisted state. It carries no sample-ID cursor or content hash:
// those are collector-owned deduplication details, not viewer evidence.
type ExternalBindingView struct {
	Check      string `json:"check"`
	Source     string `json:"source"`
	Object     string `json:"object"`
	ReportType string `json:"report_type"`
	Node       string `json:"node"`
	Service    string `json:"service,omitempty"`

	Health    string `json:"health"`
	Freshness string `json:"freshness"`
	Evidence  string `json:"evidence"`
	Reason    string `json:"reason"`

	Report *ExternalReport `json:"report,omitempty"`
	// SampledAt is the upstream sample time, not the source read time.
	SampledAt time.Time `json:"sampled_at,omitzero"`
	TimeBasis string    `json:"time_basis,omitempty"`
	// DataDeadline is original sample time + max_age. ContactDeadline is
	// last successful source contact + source.stale_seconds. ValidUntil is
	// the minimum of both and the snapshot's own deadline.
	DataDeadline    time.Time `json:"data_deadline,omitzero"`
	ContactDeadline time.Time `json:"contact_deadline,omitzero"`
	ValidUntil      time.Time `json:"valid_until,omitzero"`

	SourceState ExternalSourceView  `json:"source_state"`
	Policy      *ExternalPolicyView `json:"policy,omitempty"`
	Stats       ExternalStats       `json:"stats"`
}

type ExternalSourceView struct {
	LastContact    time.Time `json:"last_contact,omitzero"`
	LastAttemptAt  time.Time `json:"last_attempt_at,omitzero"`
	LastResult     string    `json:"last_result,omitempty"`
	LastCode       string    `json:"last_code,omitempty"`
	LastDurationMS int64     `json:"last_duration_ms,omitempty"`
}

type ExternalPolicyView struct {
	Outcome    string   `json:"outcome"`
	Violations []string `json:"violations,omitempty"`
	Missing    []string `json:"missing,omitempty"`
	Confirmed  bool     `json:"confirmed,omitempty"`
}

// ExternalStats separates report samples from collection attempts and native
// rollups. A missing upstream latency is a nil average/max, never zero.
type ExternalStats struct {
	Samples         int64 `json:"samples"`
	ResourceSamples int64 `json:"resource_samples,omitempty"`
	StatusSamples   int64 `json:"status_samples,omitempty"`

	StatusUp       int64 `json:"status_up,omitempty"`
	StatusDegraded int64 `json:"status_degraded,omitempty"`
	StatusDown     int64 `json:"status_down,omitempty"`
	StatusUnknown  int64 `json:"status_unknown,omitempty"`

	UpstreamLatencySamples int64    `json:"upstream_latency_samples,omitempty"`
	UpstreamLatencyAvgMS   *float64 `json:"upstream_latency_avg_ms,omitempty"`
	UpstreamLatencyMaxMS   *int64   `json:"upstream_latency_max_ms,omitempty"`

	Duplicates int64 `json:"duplicates,omitempty"`
	Late       int64 `json:"late,omitempty"`
	Conflicts  int64 `json:"conflicts,omitempty"`
	Unusable   int64 `json:"unusable,omitempty"`

	FirstAcceptedAt time.Time `json:"first_accepted_at,omitzero"`
	LastAcceptedAt  time.Time `json:"last_accepted_at,omitzero"`
	// ObservedForSeconds is the span between the first and last accepted
	// report. It is not collection duration or upstream response time.
	ObservedForSeconds int64 `json:"observed_for_seconds,omitempty"`
}
