# WireGuard

Collects `wg show <interface> dump` through the probe's bounded command executor.
This package owns interface/peer validation, parsing and result interpretation.

The interface private key and peer preshared keys are never retained. An old
handshake alone is not a tunnel outage. Transfer counters and handshake time
are local runtime reports; probe an inner endpoint to verify service reachability.
An absent peer is unknown. Command, permission and malformed-output failures
are monitor errors.

Tests use synthetic dump output and do not require a real WireGuard interface.
Run `go test ./internal/adapters/wireguard`.
