package monitor

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

type Input struct {
	// AllSnapshot supplies observer verdicts when Snapshot is an observer-scoped view.
	AllSnapshot *model.Snapshot
	IO          ioaccount.Snapshot
	Snapshot    model.Snapshot
	Delivery    []model.DeliveryStatus
	Unavailable bool
}
type Panel struct {
	RequestID          string      `json:"request_id"`
	Error              *FrameError `json:"error,omitempty"`
	Schema             int         `json:"schema"`
	Name               string      `json:"name"`
	ConfigRevision     string      `json:"config_revision"`
	Instance           string      `json:"instance"`
	Sequence           uint64      `json:"sequence"`
	Source             string      `json:"source"`
	Project            string      `json:"project"`
	Observer           string      `json:"observer,omitempty"`
	Lifecycle          string      `json:"lifecycle"`
	GeneratedAt        time.Time   `json:"generated_at"`
	PublisherExpiresAt time.Time   `json:"publisher_expires_at"`
	Title              string      `json:"title"`
	Icon               string      `json:"icon,omitempty"`
	BarRow             string      `json:"bar_row"`
	Rows               []Value     `json:"rows"`
}
type Value struct {
	Basis          string `json:"basis,omitempty"`
	SampleWindowMS int64  `json:"sample_window_ms,omitempty"`
	Row
	Value      any       `json:"value"`
	Unit       string    `json:"unit,omitempty"`
	Text       string    `json:"text"`
	Health     string    `json:"health"`
	Freshness  string    `json:"freshness"`
	Reason     string    `json:"reason,omitempty"`
	SampledAt  time.Time `json:"sampled_at,omitzero"`
	ValidUntil time.Time `json:"valid_until,omitzero"`
	Check      string    `json:"check,omitempty"`
	Observer   string    `json:"observer,omitempty"`
	Collector  string    `json:"collector,omitempty"`
}

func Build(c Config, in Input, now time.Time) Panel {
	p := Panel{Schema: 1, Name: c.Name, ConfigRevision: c.Revision, Source: c.Source, Project: in.Snapshot.Project, Observer: in.Snapshot.ViewObserver, Lifecycle: "running", GeneratedAt: now.UTC(), Title: c.Template.Title, Icon: c.Template.Icon, BarRow: c.Template.Bar.Row, Rows: []Value{}}
	if p.Observer == "" {
		p.Observer = c.Observer
	}
	if p.Project == "" {
		p.Project = c.Project
	}
	for _, r := range c.Template.Rows {
		p.Rows = append(p.Rows, resolve(r, c.Source, in, now))
	}
	return p
}
func missing(v Value, reason string) Value {
	v.Value = nil
	v.Text = "unknown"
	v.Health = "unknown"
	v.Reason = reason
	if v.Freshness == "fresh" || v.Freshness == "" {
		v.Freshness = "unavailable"
	}
	return v
}
func resolve(r Row, source string, in Input, now time.Time) Value {
	m, _ := metric(r.Metric)
	v := Value{Row: r, Unit: m.Unit, Text: "unknown", Health: "unknown", Freshness: "fresh"}
	if in.Unavailable {
		return missing(v, "source_unavailable")
	}
	s := in.Snapshot
	s = ageSnapshot(s, now)
	stale := map[string]int{}
	for _, check := range s.Topology.Checks {
		stale[check.ID] = check.StaleSeconds
	}
	if m.Visual == "status" || m.Visual == "list" || m.Visual == "card" {
		if s.GeneratedAt.IsZero() || s.GeneratedAt.After(now.Add(5*time.Second)) {
			return missing(v, "source_expired")
		}
		current := view.View{Snapshot: s, Now: now, Cached: view.Expired(s, now)}
		all := current
		if s.ViewObserver != "" && in.AllSnapshot == nil {
			all.Cached = true
		}
		if in.AllSnapshot != nil {
			all.Snapshot = ageSnapshot(*in.AllSnapshot, now)
			all.Cached = view.Expired(all.Snapshot, now)
		}
		return richValue(v, current, all)
	}
	if strings.HasPrefix(r.Metric, "delivery.") {
		return deliveryValue(v, in.Delivery, now)
	}
	if s.GeneratedAt.IsZero() || s.GeneratedAt.After(now.Add(5*time.Second)) || view.Expired(s, now) {
		v.Freshness = "stale"
		return missing(v, "source_expired")
	}
	if strings.HasPrefix(r.Metric, "storage.") {
		switch r.Metric {
		case "storage.journal.write_bytes":
			v.Value = in.IO.Journal.Bytes
		case "storage.checkpoint.write_bytes":
			v.Value = in.IO.Checkpoint.Bytes
		case "storage.queue.operations":
			v.Value = in.IO.Queue.Count
		case "storage.queue.write_bytes":
			v.Value = in.IO.Queue.Bytes
		case "storage.sync.count":
			v.Value = in.IO.FileSync.Count + in.IO.DirectorySync.Count
		case "storage.sync.duration_ms":
			v.Value = float64(in.IO.FileSync.Nanoseconds+in.IO.DirectorySync.Nanoseconds) / 1e6
		}
		v.Text = fmt.Sprint(v.Value)
		v.Health = "up"
		v.SampledAt = s.GeneratedAt
		v.ValidUntil = s.GeneratedAt.Add(15 * time.Second)
		return v
	}
	if m.Selector == "" {
		verdict := (view.View{Snapshot: s, Now: now}).Verdict()
		v.Value = verdict.State
		v.Text = verdict.Text
		v.Health = verdictHealth(verdict.State)
		v.ValidUntil = s.GeneratedAt.Add(15 * time.Second)
		for _, cs := range s.Checks {
			if cs.Last != nil {
				if cs.Last.SampledAt.After(v.SampledAt) {
					v.SampledAt = cs.Last.SampledAt
				}
				expires := cs.Last.SampledAt.Add(time.Duration(stale[cs.ID]) * time.Second)
				if cs.Freshness == "fresh" && expires.Before(v.ValidUntil) {
					v.ValidUntil = expires
				}
			}
		}
		return v
	}
	var matches []model.CheckState
	for _, cs := range s.Checks {
		if r.Select.Check != "" && cs.ID != r.Select.Check {
			continue
		}
		if m.Fact != "" {
			host := false
			for _, check := range s.Topology.Checks {
				if check.ID == cs.ID {
					host = check.Kind == "host"
					break
				}
			}
			if !host {
				continue
			}
		}
		matches = append(matches, cs)
	}
	if len(matches) == 0 {
		return missing(v, "entity_missing")
	}
	if len(matches) > 1 {
		return missing(v, "selector_ambiguous")
	}
	cs := matches[0]
	v.Check = cs.ID
	v.Observer = cs.Observer
	v.Health = cs.Health
	v.Freshness = cs.Freshness
	if cs.Last == nil {
		return missing(v, "never_sampled")
	}
	v.SampledAt = cs.Last.SampledAt
	for _, check := range s.Topology.Checks {
		if check.ID == cs.ID {
			v.ValidUntil = v.SampledAt.Add(time.Duration(check.StaleSeconds) * time.Second)
			break
		}
	}
	if limit := s.GeneratedAt.Add(15 * time.Second); v.ValidUntil.After(limit) {
		v.ValidUntil = limit
	}
	if cs.Freshness != "fresh" {
		return missing(v, cs.Freshness)
	}
	if v.SampledAt.After(now.Add(5 * time.Second)) {
		return missing(v, "clock_skew")
	}
	if v.ValidUntil.IsZero() || !now.Before(v.ValidUntil) {
		v.Freshness = "stale"
		return missing(v, "sample_expired")
	}
	if r.Metric == "check.status" {
		v.Value = cs.Health
		v.Text = cs.Health
		return v
	}
	resource, ok := (view.View{Snapshot: s, Now: now}).HostResource(cs.ID)
	if !ok || resource.Facts == nil {
		return missing(v, "sample_unreadable")
	}
	n, e := strconv.ParseFloat(resource.Facts[m.Fact], 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
		return missing(v, "invalid_value")
	}
	v.Value = n
	switch r.Metric {
	case "host.cpu.used_percent":
		v.Basis = "host_normalized_0_100"
		v.SampleWindowMS, _ = strconv.ParseInt(resource.Facts["cpu_sample_ms"], 10, 64)
	case "host.memory.used_percent":
		v.Basis = resource.Facts["memory_basis"]
	case "host.disk.used_percent":
		v.Basis = resource.Facts["disk_basis"]
	}
	v.Text = fmt.Sprintf("%.1f%%", n)
	return v
}
func deliveryValue(v Value, reports []model.DeliveryStatus, now time.Time) Value {
	var matches []model.DeliveryStatus
	for _, r := range reports {
		if v.Select.Collector == "" || v.Select.Collector == r.Collector {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return missing(v, "never_reported")
	}
	if len(matches) > 1 {
		return missing(v, "selector_ambiguous")
	}
	r := matches[0]
	v.Collector = r.Collector
	v.SampledAt = r.ReportedAt
	v.ValidUntil = r.ReportedAt.Add(15 * time.Second)
	if r.ReportedAt.After(now.Add(5 * time.Second)) {
		return missing(v, "clock_skew")
	}
	if !now.Before(v.ValidUntil) {
		v.Freshness = "stale"
		return missing(v, "delivery_expired")
	}
	v.Health = "up"
	if r.Code != "" {
		v.Health = "degraded"
	}
	switch v.Metric {
	case "delivery.status":
		v.Value = r.Code
		if r.Code == "" {
			v.Value = "ready"
		}
		v.Text = v.Value.(string)
	case "delivery.pending_records":
		v.Value = r.PendingRecords
		v.Text = strconv.Itoa(r.PendingRecords)
	case "delivery.pending_bytes":
		v.Value = r.PendingBytes
		v.Text = strconv.FormatInt(r.PendingBytes, 10) + " B"
	}
	return v
}

// ageSnapshot preserves collector judgments while allowing check and observer
// evidence to expire between source refreshes. It never mutates its input.
func ageSnapshot(s model.Snapshot, now time.Time) model.Snapshot {
	s.Checks = slices.Clone(s.Checks)
	stale := map[string]int{}
	for _, c := range s.Topology.Checks {
		stale[c.ID] = c.StaleSeconds
	}
	for i := range s.Checks {
		c := &s.Checks[i]
		if c.Freshness == "fresh" && c.Last != nil && (c.Last.SampledAt.After(now.Add(5*time.Second)) || !now.Before(c.Last.SampledAt.Add(time.Duration(stale[c.ID])*time.Second))) {
			c.Freshness = "stale"
			c.Health = "unknown"
		}
	}
	s.Observers = slices.Clone(s.Observers)
	for i := range s.Observers {
		o := &s.Observers[i]
		for _, d := range s.Topology.Observers {
			if d.ID == o.ID && o.Health == "up" && (o.LastSeen.After(now.Add(5*time.Second)) || !now.Before(o.LastSeen.Add(time.Duration(d.StaleSeconds)*time.Second))) {
				o.Health = "unknown"
			}
		}
	}
	return s
}

// FrameError describes a rejected stream request without exposing upstream details.
type FrameError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
