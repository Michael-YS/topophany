# WireGuard

Collects `wg show <interface> latest-handshakes` and `transfer` through the probe's bounded command executor.
This package owns interface/peer validation, parsing and result interpretation.

Private keys and preshared keys are never requested. The separate command reads
are not an atomic snapshot; a peer missing from only one read remains unknown. An old
handshake alone is not a tunnel outage. Transfer counters and handshake time
are local runtime reports; probe an inner endpoint to verify service reachability.
An absent peer is unknown. Command, permission and malformed-output failures
are monitor errors.

Tests use synthetic dump output and do not require a real WireGuard interface.
Run `go test ./internal/adapters/wireguard`.

Optional `adapter.expected_allowed_ips` asserts a nonempty set of 1..128
canonicalized CIDRs; ordering does not matter. `expected_keepalive_seconds`
asserts 0..65535 seconds, with 0 meaning off. Each adds only its specific public
field query. These assertions verify runtime configuration, not OS routes.
No handshake-age threshold marks an idle tunnel down.

Each query is limited to 2 MiB and 4096 peer rows. Malformed or duplicate rows
are monitor errors. Interface selectors `all`, `interfaces` and flag-like names
are refused. Versioned synthetic TSV fixtures are described in `testdata/`.
