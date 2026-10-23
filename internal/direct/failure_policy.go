package direct

// One policy supplies both the public retryable flag and the acquisition loop.
// Backoff and Retry-After remain session-owned; authentication needs user action.
func failurePolicy(code string) (state string, retry bool) {
	switch code {
	case "auth_required":
		return "auth_required", false
	case "forbidden":
		return "forbidden", false
	case "connection_failed", "tls_error", "rate_limited", "credential_renewed":
		return "retrying", true
	default:
		return "error", false
	}
}
