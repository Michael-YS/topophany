package client

import "github.com/WhitenWhiten/topophany/internal/httpfailure"

// DeliveryAction distinguishes endpoint authorization from rejection of data.
// Waiting for credentials preserves pending samples and uses bounded backoff.
type DeliveryAction uint8

const (
	RetryDelivery DeliveryAction = iota
	WaitForAuthorization
	RejectSample
	StorageBackpressure
)

func (e *HTTPError) DeliveryAction() DeliveryAction {
	// The collector distinguishes a revoked credential from a sample naming a
	// removed/foreign check. Only the latter explicitly rejects the payload.
	if e.Status == 403 && e.Code == "observer_scope_violation" {
		return RejectSample
	}

	if e.Status == 429 && e.Code == "observer_storage_quota" || e.Status == 503 && e.Code == "storage_receipt_capacity" {
		return StorageBackpressure
	}
	switch httpfailure.Classify(e.Status) {
	case httpfailure.Authentication, httpfailure.Permission:
		return WaitForAuthorization
	case httpfailure.Rejected:
		return RejectSample
	default:
		return RetryDelivery
	}
}
