package monitor

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

type Input struct {
	Snapshot    model.Snapshot
	Delivery    []model.DeliveryStatus
	Unavailable bool
}
type Panel struct {
	Schema             int       `json:"schema"`
	Name               string    `json:"name"`
	ConfigRevision     string    `json:"config_revision"`
	Instance           string    `json:"instance"`
	Sequence           uint64    `json:"sequence"`
	Source             string    `json:"source"`
	Project            string    `json:"project"`
	Observer           string    `json:"observer,omitempty"`
	Lifecycle          string    `json:"lifecycle"`
	GeneratedAt        time.Time `json:"generated_at"`
	PublisherExpiresAt time.Time `json:"publisher_expires_at"`
	Title              string    `json:"title"`
	Icon               string    `json:"icon,omitempty"`
	BarRow             string    `json:"bar_row"`
	Rows               []Value   `json:"rows"`
}
type Value struct {
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
	if strings.HasPrefix(r.Metric, "delivery.") {
		return deliveryValue(v, in.Delivery, now)
	}
	if s.GeneratedAt.IsZero() || s.GeneratedAt.After(now.Add(5*time.Second)) || view.Expired(s, now) {
		v.Freshness = "stale"
		return missing(v, "source_expired")
	}
	if m.Selector == "" {
		verdict := (view.View{Snapshot: s, Now: now}).Verdict()
		v.Value = verdict.State
		v.Text = verdict.Text
		v.Health = verdict.State
		v.SampledAt = s.GeneratedAt
		v.ValidUntil = s.GeneratedAt.Add(15 * time.Second)
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
		v.Health = "warn"
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
