package client

import "testing"

func TestDeliveryFailureActions(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		want   DeliveryAction
	}{
		{401, "invalid_token", WaitForAuthorization}, {403, "role_forbidden", WaitForAuthorization},
		{403, "observer_scope_violation", RejectSample},
		{408, "", RetryDelivery}, {429, "rate_limited", RetryDelivery}, {522, "", RetryDelivery},
		{409, "contract_mismatch", RejectSample}, {422, "sample_time_out_of_range", RejectSample},
		{429, "observer_storage_quota", StorageBackpressure}, {503, "storage_receipt_capacity", StorageBackpressure},
		{400, "storage_receipt_capacity", RejectSample}, {503, "storage_unavailable", RetryDelivery},
	} {
		if got := (&HTTPError{Status: tc.status, Code: tc.code}).DeliveryAction(); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
}
