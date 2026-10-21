package outbox

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"slices"
	"strings"
)

func (q *Queue) deliveryStats(recipient string) (int, int64, int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	if p := q.pending[recipient]; p != nil {
		n = p.Len()
	}
	return n, q.recipientBytes[recipient], q.quarantineUsed
}

// Statuses reads immutable reports without racing destination retry state.
func (d *Delivery) Statuses() []model.DeliveryStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]model.DeliveryStatus, 0, len(d.reports))
	for _, r := range d.reports {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b model.DeliveryStatus) int { return strings.Compare(a.Collector, b.Collector) })
	return out
}
func deliveryCode(err error) string {
	var h *client.HTTPError
	if !errors.As(err, &h) {
		return "collector_unreachable"
	}
	if h.Status == 401 || h.Status == 403 {
		return "authorization_rejected"
	}
	if h.Code == "observer_storage_quota" || h.Code == "storage_receipt_capacity" {
		return "storage_backpressure"
	}
	if h.Permanent() {
		return "sample_rejected"
	}
	return "collector_unreachable"
}
func (d *Delivery) publish(q *Queue, dest *destination, result Result) {
	id := Recipient(dest.endpoint.URL)
	n, bytes, quarantine := q.deliveryStats(id)
	if result.Accepted > 0 {
		dest.lastAck = d.Now()
	}
	dest.quarantined += int64(result.Quarantined)
	report := model.DeliveryStatus{Collector: id, ReportedAt: d.Now().UTC(), PendingRecords: n, PendingBytes: bytes, QuarantineBytes: quarantine, QuarantinedSamples: dest.quarantined, LastAck: dest.lastAck, LastContact: dest.lastContact, RetryAt: dest.next, Code: dest.code}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reports == nil {
		d.reports = map[string]model.DeliveryStatus{}
	}
	d.reports[id] = report
}

// diagnosticCode preserves actionable protocol failures without logging arbitrary
// response text. Wire delivery health retains its existing broader categories.
func diagnosticCode(err error) string {
	var h *client.HTTPError
	if errors.As(err, &h) {
		switch h.Code {
		case "contract_mismatch", "observation_contract_mismatch", "revision_mismatch", "project_mismatch",
			"observer_scope_violation", "sample_time_out_of_range", "unsupported_batch_schema",
			"invalid_batch_revision", "invalid_batch_size", "invalid_observation_identity",
			"received_at_is_collector_owned", "invalid_outcome", "evidence_kind_mismatch",
			"heartbeat_limit_exceeded", "invalid_delivery_status", "invalid_batch_json",
			"observer_storage_quota", "storage_receipt_capacity", "authentication_required", "invalid_token", "role_forbidden":
			return h.Code
		}
	}
	return deliveryCode(err)
}
